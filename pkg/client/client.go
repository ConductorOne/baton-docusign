package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// API endpoint constants.
const (
	getUsers              = "/restapi/v2.1/accounts/%s/users"
	getGroups             = "/restapi/v2.1/accounts/%s/groups"
	getSigningGroups      = "/restapi/v2.1/accounts/%s/signing_groups"
	getPermissions        = "/restapi/v2.1/accounts/%s/users/%s"
	getGroupUsers         = "/restapi/v2.1/accounts/%s/groups/%s/users"
	createUsers           = "/restapi/v2.1/accounts/%s/users"
	getPermissionProfiles = "/restapi/v2.1/accounts/%s/permission_profiles"
)

// OAuth User Info endpoints.
const (
	userInfoEndpointDemo = "https://account-d.docusign.com/oauth/userinfo"
	userInfoEndpointProd = "https://account.docusign.com/oauth/userinfo"
)

// Client wraps HTTP interactions with the DocuSign API, handling auth and base URL.
type Client struct {
	isDemo         bool
	tokenSource    oauth2.TokenSource
	wrapper        *uhttp.BaseHttpClient
	baseURI        string
	accountId      string
	userInfo       *UserInfoResponse
	userInfoExpiry time.Time
	mutex          sync.RWMutex // protects baseURI, accountId, userInfo, and userInfoExpiry
}

// New constructs a Client with OAuth2 flow, now using dynamic base URI resolution.
func New(ctx context.Context, isDemo bool, clientID, clientSecret, redirectURI, refreshToken string) (*Client, error) {
	tokenSource := getTokenSource(ctx, clientID, clientSecret, redirectURI, refreshToken)
	baseClient := oauth2.NewClient(ctx, tokenSource)

	return &Client{
		isDemo:      isDemo,
		tokenSource: tokenSource,
		wrapper:     uhttp.NewBaseHttpClient(baseClient),
	}, nil
}

// NewClient initializes a Client with a fixed token and optional HTTP wrapper.
func NewClient(ctx context.Context, isDemo bool, tokenSource oauth2.TokenSource, httpClient ...*uhttp.BaseHttpClient) *Client {
	var wrapper *uhttp.BaseHttpClient
	if len(httpClient) > 0 {
		wrapper = httpClient[0]
	} else {
		baseClient := oauth2.NewClient(ctx, tokenSource)
		wrapper = uhttp.NewBaseHttpClient(baseClient)
	}

	return &Client{
		isDemo:      isDemo,
		tokenSource: tokenSource,
		wrapper:     wrapper,
	}
}

// fetchUserInfo calls the DocuSign OAuth User Info endpoint to get account details.
func (c *Client) fetchUserInfo(ctx context.Context) (*UserInfoResponse, error) {
	var userInfoEndpoint string
	if c.isDemo {
		userInfoEndpoint = userInfoEndpointDemo
	} else {
		userInfoEndpoint = userInfoEndpointProd
	}

	userInfoURL, err := url.Parse(userInfoEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid user info endpoint: %w", err)
	}

	var userInfo UserInfoResponse
	_, _, err = c.doRequest(ctx, http.MethodGet, userInfoURL, &userInfo)
	if err != nil {
		// Wrap the error so baton-sdk can handle retries
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	if len(userInfo.Accounts) == 0 {
		return nil, fmt.Errorf("no accounts found in user info response")
	}

	return &userInfo, nil
}

// ensureInitialized ensures the client has fetched user info and set base URI.
func (c *Client) ensureInitialized(ctx context.Context) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if we have valid cached user info
	if c.userInfo != nil && time.Now().Before(c.userInfoExpiry) {
		return nil
	}

	// Fetch fresh user info
	userInfo, err := c.fetchUserInfo(ctx)
	if err != nil {
		return err
	}

	// Find the appropriate account
	var selectedAccount *AccountInfo
	for _, account := range userInfo.Accounts {
		if account.IsDefault {
			selectedAccount = &account
			break
		}
	}

	// If no default account, use the first one
	if selectedAccount == nil && len(userInfo.Accounts) > 0 {
		selectedAccount = &userInfo.Accounts[0]
	}

	if selectedAccount == nil {
		return fmt.Errorf("no valid account found in user info")
	}

	// Update cached values with 1-hour expiry
	c.userInfo = userInfo
	c.userInfoExpiry = time.Now().Add(1 * time.Hour)
	c.baseURI = selectedAccount.BaseURI
	c.accountId = selectedAccount.AccountId

	return nil
}

// getClientURL safely reads baseURI and accountId to build a URL.
func (c *Client) getClientURL(path string, params ...interface{}) (*url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	return buildURL(baseURI, path, append([]interface{}{accountId}, params...)...)
}

// getClientBaseURL safely reads baseURI for URL parsing.
func (c *Client) getClientBaseURL() (*url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	c.mutex.RUnlock()

	return url.Parse(baseURI)
}

// getClientAccountId safely reads accountId.
func (c *Client) getClientAccountId() string {
	c.mutex.RLock()
	accountId := c.accountId
	c.mutex.RUnlock()

	return accountId
}

// GetUsers fetches a page of users and returns users, next page token, and annotations.
func (c *Client) GetUsers(ctx context.Context, options PageOptions) ([]User, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var usersResponse UsersResponse

	baseURL, err := c.getClientBaseURL()
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	usersURL, err := preparePagedRequest(baseURL, fmt.Sprintf(getUsers, c.getClientAccountId()), options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, usersURL, &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(usersResponse.Page)

	return usersResponse.Users, nextToken, annos, nil
}

// GetGroups fetches a page of groups and handles pagination and rate limit annotations.
func (c *Client) GetGroups(ctx context.Context, options PageOptions) ([]Group, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var groupsResponse GroupsResponse

	baseURL, err := c.getClientBaseURL()
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	groupsURL, err := preparePagedRequest(baseURL, fmt.Sprintf(getGroups, c.getClientAccountId()), options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, groupsURL, &groupsResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(groupsResponse.Page)

	return groupsResponse.Groups, nextToken, annos, nil
}

// GetGroupUsers fetches users for a group with pagination support.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string, options PageOptions) ([]User, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var usersResponse UsersResponse

	baseURL, err := c.getClientBaseURL()
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	groupUsersURL, err := preparePagedRequest(baseURL, fmt.Sprintf(getGroupUsers, c.getClientAccountId(), groupId), options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, groupUsersURL, &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(usersResponse.Page)

	return usersResponse.Users, nextToken, annos, nil
}

// GetUserDetails fetches detailed information for a specific user, including permissions.
func (c *Client) GetUserDetails(ctx context.Context, userID string) (*UserDetail, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	userURL, err := c.getClientURL(getPermissions, userID)
	if err != nil {
		return nil, nil, err
	}

	var userDetail UserDetail
	_, annos, err := c.doRequest(ctx, http.MethodGet, userURL, &userDetail)
	if err != nil {
		return nil, annos, fmt.Errorf("error fetching user details: %w", err)
	}

	return &userDetail, annos, nil
}

// CreateUsers sends a bulk create request for new users in the account.
func (c *Client) CreateUsers(ctx context.Context, request CreateUsersRequest) (*UserCreationResponse, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	if len(request.NewUsers) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	createUsersURL, err := c.getClientURL(createUsers)
	if err != nil {
		return nil, nil, err
	}

	var response UserCreationResponse
	_, annon, err := c.doRequestWithBody(ctx, http.MethodPost, createUsersURL.String(), request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error creating users: %w", err)
	}

	return &response, annon, nil
}

func (c *Client) GetSigningGroups(ctx context.Context, options PageOptions) ([]SigningGroup, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var signingGroupsResponse SigningGroupResponse

	baseURL, err := c.getClientBaseURL()
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	signingGroupsURL, err := preparePagedRequest(baseURL, fmt.Sprintf(getSigningGroups, c.getClientAccountId()), options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, signingGroupsURL, &signingGroupsResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(signingGroupsResponse.Page)

	return signingGroupsResponse.SigningGroups, nextToken, annos, nil
}

func (c *Client) GetSigningGroupUsers(ctx context.Context, groupId string, options PageOptions) ([]User, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var groupMembersResponse UsersResponse

	baseURL, err := c.getClientBaseURL()
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	getSignedGroupDetailsURL, err := url.JoinPath(fmt.Sprintf(getSigningGroups, c.getClientAccountId()), groupId)
	if err != nil {
		return nil, "", nil, err
	}

	signedGroupDetailsURL, err := preparePagedRequest(baseURL, getSignedGroupDetailsURL, options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, signedGroupDetailsURL, &groupMembersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(groupMembersResponse.Page)

	return groupMembersResponse.Users, nextToken, annos, nil
}

// GetUserByEmail retrieves a user filtering by the email and the user status 'Active' or 'Activation Sent'.
func (c *Client) GetUserByEmail(ctx context.Context, userEmail string) (*User, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	userURL, err := c.getClientURL(getUsers)
	if err != nil {
		return nil, nil, err
	}
	ApplyQueryParam(userURL, "status", "Active,ActivationSent")
	ApplyQueryParam(userURL, "email", userEmail)

	var usersResponse UsersResponse
	_, annos, err := c.doRequest(ctx, http.MethodGet, userURL, &usersResponse)
	if err != nil {
		return nil, annos, fmt.Errorf("error fetching user details: %w", err)
	}

	if len(usersResponse.Users) != 1 {
		return nil, annos, fmt.Errorf("error fetching user details. Got %d users with the email %s", len(usersResponse.Users), userEmail)
	}
	user := usersResponse.Users[0]

	return &user, annos, nil
}

func (c *Client) GetPermissionProfiles(ctx context.Context) ([]PermissionProfile, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	var permissionProfilesResponse PermissionProfilesResponse

	baseURL, err := c.getClientBaseURL()
	if err != nil {
		return nil, nil, fmt.Errorf("invalid base URL: %w", err)
	}

	permissionProfilesURL, err := url.Parse(fmt.Sprintf(getPermissionProfiles, c.getClientAccountId()))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid endpoint: %w", err)
	}

	permissionProfilesURL = baseURL.ResolveReference(permissionProfilesURL)

	_, annos, err := c.doRequest(ctx, http.MethodGet, permissionProfilesURL, &permissionProfilesResponse)
	if err != nil {
		return nil, nil, err
	}

	return permissionProfilesResponse.PermissionProfiles, annos, nil
}

// doRequestWithBody builds and executes a JSON POST/PUT request and decodes the response.
func (c *Client) doRequestWithBody(
	ctx context.Context,
	method string,
	requestURL string,
	body interface{},
	res interface{},
) (http.Header, annotations.Annotations, error) {
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return nil, nil, err
	}
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, nil, err
	}
	req, err := c.wrapper.NewRequest(
		ctx,
		method,
		parsedURL,
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(token.AccessToken),
		uhttp.WithJSONBody(body),
	)
	if err != nil {
		return nil, nil, err
	}

	return doRequestCommon(c.wrapper, req, res)
}

// doRequest builds and executes an HTTP request without a body, decoding JSON response if provided.
func (c *Client) doRequest(ctx context.Context, method string, url *url.URL, response interface{}) (http.Header, annotations.Annotations, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, nil, err
	}

	req, err := c.wrapper.NewRequest(
		ctx,
		method,
		url,
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(token.AccessToken),
	)
	if err != nil {
		return nil, nil, err
	}

	return doRequestCommon(c.wrapper, req, response)
}
