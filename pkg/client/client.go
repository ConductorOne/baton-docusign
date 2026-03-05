// Package client provides a wrapper for interacting with the DocuSign API.
//
// # API Endpoints Used
//
// This client interacts with the following DocuSign eSignature REST API v2.1 endpoints:
//
// Users:
// DOCUMENTATION: https://developers.docusign.com/docs/esign-rest-api/reference/users/users/
//   - GET    /restapi/v2.1/accounts/{accountId}/users - List account users (supports pagination)
//   - GET    /restapi/v2.1/accounts/{accountId}/users/{userId} - Get user details
//   - POST   /restapi/v2.1/accounts/{accountId}/users - Create new users
//   - PUT    /restapi/v2.1/accounts/{accountId}/users/{userId}/profile - Update user profile
//   - DELETE /restapi/v2.1/accounts/{accountId}/users - Delete users
//
// Groups:
// DOCUMENTATION: https://developers.docusign.com/docs/esign-rest-api/reference/usergroups/groups/
// DOCUMENTATION: https://developers.docusign.com/docs/esign-rest-api/reference/usergroups/groupusers/
//   - GET    /restapi/v2.1/accounts/{accountId}/groups - List account groups (supports pagination)
//   - GET    /restapi/v2.1/accounts/{accountId}/groups/{groupId}/users - List users in a group (supports pagination)
//   - PUT    /restapi/v2.1/accounts/{accountId}/groups/{groupId}/users - Add users to a group
//   - DELETE /restapi/v2.1/accounts/{accountId}/groups/{groupId}/users - Remove users from a group
//
// Signing Groups:
// DOCUMENTATION: https://developers.docusign.com/docs/esign-rest-api/reference/signinggroups/
//   - GET    /restapi/v2.1/accounts/{accountId}/signing_groups - List signing groups (supports pagination)
//   - GET    /restapi/v2.1/accounts/{accountId}/signing_groups/{groupId}/users - List signing group users (supports pagination)
//   - PUT    /restapi/v2.1/accounts/{accountId}/signing_groups/{groupId} - Update signing group membership
//   - DELETE /restapi/v2.1/accounts/{accountId}/signing_groups/{groupId}/users - Remove users from signing group
//
// Permission Profiles:
// DOCUMENTATION: https://developers.docusign.com/docs/esign-rest-api/reference/accounts/accountpermissionprofiles/
// DOCUMENTATION: https://developers.docusign.com/docs/esign-rest-api/reference/users/userprofiles/
//   - GET /restapi/v2.1/accounts/{accountId}/permission_profiles - List permission profiles (no pagination)
//   - PUT /restapi/v2.1/accounts/{accountId}/users/{userId}/profile - Update user profile
//
// OAuth:
//   - GET https://account-d.docusign.com/oauth/userinfo - Get OAuth user info (demo environment)
//   - GET https://account.docusign.com/oauth/userinfo - Get OAuth user info (production environment)
//
// # API Documentation
//
// Complete API documentation: https://developers.docusign.com/docs/esign-rest-api/reference/
//
// # Pagination Strategy
//
// DocuSign API uses cursor-based pagination with the following parameters:
//   - count: Number of results per page (1-100, default: 100)
//   - start_position: Starting position (0-based index)
//
// The API returns:
//   - endPosition: Last item position in current page
//   - totalSetSize: Total number of items available
//
// Rate Limiting:
//   - API rate limits are enforced by DocuSign
//   - The SDK automatically handles rate limit errors via uhttp
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// API endpoint constants.
const (
	getUsers                = "/restapi/v2.1/accounts/%s/users"
	getGroups               = "/restapi/v2.1/accounts/%s/groups"
	getSigningGroups        = "/restapi/v2.1/accounts/%s/signing_groups"
	getUserDetails          = "/restapi/v2.1/accounts/%s/users/%s"
	getGroupUsers           = "/restapi/v2.1/accounts/%s/groups/%s/users"
	createUsers             = "/restapi/v2.1/accounts/%s/users"
	deleteUsers             = "/restapi/v2.1/accounts/%s/users"
	getPermissionProfiles   = "/restapi/v2.1/accounts/%s/permission_profiles"
	updateGroupUsers        = "/restapi/v2.1/accounts/%s/groups/%s/users"
	deleteGroupUsers        = "/restapi/v2.1/accounts/%s/groups/%s/users"
	updateSigningGroupUsers = "/restapi/v2.1/accounts/%s/signing_groups/%s"
	deleteSigningGroupUsers = "/restapi/v2.1/accounts/%s/signing_groups/%s/users"
	updateUserProfile       = "/restapi/v2.1/accounts/%s/users/%s/profile"
)

// OAuth User Info endpoints.
const (
	userInfoEndpointDemo = "https://account-d.docusign.com/oauth/userinfo"
	userInfoEndpointProd = "https://account.docusign.com/oauth/userinfo"
)

// Client wraps HTTP interactions with the DocuSign API, handling auth and base URL.
type Client struct {
	isDemo          bool
	configAccountId string // user-specified account ID from config (empty = use default)
	tokenSource     oauth2.TokenSource
	wrapper         *uhttp.BaseHttpClient
	baseURI         string
	accountId       string
	userInfo        *UserInfoResponse
	userInfoExpiry  time.Time
	mutex           sync.RWMutex // protects baseURI, accountId, userInfo, and userInfoExpiry
}

// New constructs a Client with OAuth2 flow, now using dynamic base URI resolution.
// If configAccountId is non-empty, the client will select that specific account instead of the default.
func New(ctx context.Context, isDemo bool, clientID, clientSecret, redirectURI, refreshToken, configAccountId string) (*Client, error) {
	tokenSource := getTokenSource(ctx, isDemo, clientID, clientSecret, redirectURI, refreshToken)
	baseClient := oauth2.NewClient(ctx, tokenSource)

	return &Client{
		isDemo:          isDemo,
		configAccountId: configAccountId,
		tokenSource:     tokenSource,
		wrapper:         uhttp.NewBaseHttpClient(baseClient),
	}, nil
}

// RequestRefreshToken exchanges an authorization code for a refresh token.
// DocuSign requires Basic Auth (base64(clientID:clientSecret)) and redirect_uri in the request.
func (c *Client) RequestRefreshToken(ctx context.Context, clientID, clientSecret, redirectURI, code string) (string, error) {
	// Select the appropriate token endpoint based on demo/production environment
	tokenEndpoint := tokenURLProd
	if c.isDemo {
		tokenEndpoint = tokenURLDemo
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	// DocuSign requires Basic Auth: base64(clientID:clientSecret)
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Use a basic HTTP client instead of c.wrapper to avoid OAuth2 authentication issues
	// during the token exchange (we don't have a valid token yet)
	basicClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	baseWrapper := uhttp.NewBaseHttpClient(basicClient)

	// TODO: We don't need to create this here. We could just use the struct TokenResponse created on models.go
	var target struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}

	res, err := baseWrapper.Do(req, uhttp.WithJSONResponse(&target))
	if err != nil {
		return "", err
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error getting refresh token (status %s): %v", res.Status, target)
	}

	// Validate that we received a non-empty refresh token
	if target.RefreshToken == "" {
		return "", fmt.Errorf("received empty refresh token from DocuSign")
	}

	return target.RefreshToken, nil
}

// NewClient initializes a Client with a fixed token and optional HTTP wrapper.
// If configAccountId is non-empty, the client will select that specific account instead of the default.
func NewClient(ctx context.Context, isDemo bool, tokenSource oauth2.TokenSource, configAccountId string, httpClient ...*uhttp.BaseHttpClient) *Client {
	var wrapper *uhttp.BaseHttpClient
	if len(httpClient) > 0 {
		wrapper = httpClient[0]
	} else {
		baseClient := oauth2.NewClient(ctx, tokenSource)
		wrapper = uhttp.NewBaseHttpClient(baseClient)
	}

	return &Client{
		isDemo:          isDemo,
		configAccountId: configAccountId,
		tokenSource:     tokenSource,
		wrapper:         wrapper,
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
	_, _, err = c.doRequest(ctx, http.MethodGet, userInfoURL, nil, &userInfo)
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
	if c.configAccountId != "" {
		// User specified an account ID — find the matching account
		for _, account := range userInfo.Accounts {
			if account.AccountId == c.configAccountId {
				selectedAccount = &account
				break
			}
		}
		if selectedAccount == nil {
			availableIDs := make([]string, 0, len(userInfo.Accounts))
			for _, account := range userInfo.Accounts {
				availableIDs = append(availableIDs, account.AccountId)
			}
			return fmt.Errorf("configured account ID %q not found in user info; available account IDs: %v", c.configAccountId, availableIDs)
		}
	} else {
		// No account ID configured — use default or first account
		for _, account := range userInfo.Accounts {
			if account.IsDefault {
				selectedAccount = &account
				break
			}
		}
		if selectedAccount == nil && len(userInfo.Accounts) > 0 {
			selectedAccount = &userInfo.Accounts[0]
		}
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

// buildClientURL safely reads baseURI and accountId to build a URL.
func (c *Client) buildClientURL(path string, params ...any) (*url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	return buildURL(baseURI, path, append([]any{accountId}, params...)...)
}

// prepareClientPagedRequest safely prepares a paged request URL with client's baseURI and accountId.
func (c *Client) prepareClientPagedRequest(endpoint string, options PageOptions) (*url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	baseURL, err := url.Parse(baseURI)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	return preparePagedRequest(baseURL, fmt.Sprintf(endpoint, accountId), options)
}

// prepareSigningGroupUsersRequest handles the special case for signing group users.
func (c *Client) prepareSigningGroupUsersRequest(groupId string, options PageOptions) (*url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	baseURL, err := url.Parse(baseURI)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	getSignedGroupDetailsURL, err := url.JoinPath(fmt.Sprintf(getSigningGroups, accountId), groupId)
	if err != nil {
		return nil, err
	}

	return preparePagedRequest(baseURL, getSignedGroupDetailsURL, options)
}

// buildPermissionProfilesURL handles the special case for permission profiles.
func (c *Client) buildPermissionProfilesURL() (*url.URL, *url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	baseURL, err := url.Parse(baseURI)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid base URL: %w", err)
	}

	permissionProfilesURL, err := url.Parse(fmt.Sprintf(getPermissionProfiles, accountId))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid endpoint: %w", err)
	}

	return baseURL, permissionProfilesURL, nil
}

// prepareGroupUsersRequest handles paged requests for group users.
func (c *Client) prepareGroupUsersRequest(groupId string, options PageOptions) (*url.URL, error) {
	c.mutex.RLock()
	baseURI := c.baseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	baseURL, err := url.Parse(baseURI)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	// Build the endpoint with both accountId and groupId
	endpoint := fmt.Sprintf(getGroupUsers, accountId, groupId)
	return preparePagedRequest(baseURL, endpoint, options)
}

// GetUsers fetches a page of users from the DocuSign account.
//
// Pagination: This endpoint supports cursor-based pagination using start_position and count parameters.
// The API returns up to 100 users per page (controlled by PageOptions.PageSize).
// To fetch the next page, use the returned nextToken as PageOptions.PageToken.
//
// Returns: users list, next page token (empty if last page), annotations, error.
func (c *Client) GetUsers(ctx context.Context, options PageOptions) ([]User, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var usersResponse UsersResponse

	usersURL, err := c.prepareClientPagedRequest(getUsers, options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, usersURL, nil, &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(usersResponse.Page)

	return usersResponse.Users, nextToken, annos, nil
}

// GetGroups fetches a page of groups from the DocuSign account.
//
// Pagination: This endpoint supports cursor-based pagination using start_position and count parameters.
// The API returns up to 100 groups per page (controlled by PageOptions.PageSize).
// To fetch the next page, use the returned nextToken as PageOptions.PageToken.
//
// Returns: groups list, next page token (empty if last page), annotations, error.
func (c *Client) GetGroups(ctx context.Context, options PageOptions) ([]Group, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var groupsResponse GroupsResponse

	groupsURL, err := c.prepareClientPagedRequest(getGroups, options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, groupsURL, nil, &groupsResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(groupsResponse.Page)

	return groupsResponse.Groups, nextToken, annos, nil
}

// GetGroupUsers fetches a page of users that belong to a specific group.
//
// Pagination: This endpoint supports cursor-based pagination using start_position and count parameters.
// The API returns up to 100 users per page (controlled by PageOptions.PageSize).
// To fetch the next page, use the returned nextToken as PageOptions.PageToken.
//
// Returns: users list, next page token (empty if last page), annotations, error.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string, options PageOptions) ([]User, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var usersResponse UsersResponse

	groupUsersURL, err := c.prepareGroupUsersRequest(groupId, options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, groupUsersURL, nil, &usersResponse)
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

	userURL, err := c.buildClientURL(getUserDetails, userID)
	if err != nil {
		return nil, nil, err
	}

	var userDetail UserDetail
	_, annos, err := c.doRequest(ctx, http.MethodGet, userURL, nil, &userDetail)
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

	createUsersURL, err := c.buildClientURL(createUsers)
	if err != nil {
		return nil, nil, err
	}

	var response UserCreationResponse
	_, annon, err := c.doRequest(ctx, http.MethodPost, createUsersURL, request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error creating users: %w", err)
	}

	return &response, annon, nil
}

// DeleteUsers sends a bulk delete request to remove users from the account.
func (c *Client) DeleteUsers(ctx context.Context, request DeleteUsersRequest) (*DeleteUsersResponse, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	if len(request.Users) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	deleteUsersURL, err := c.buildClientURL(deleteUsers)
	if err != nil {
		return nil, nil, err
	}

	var response DeleteUsersResponse
	_, annon, err := c.doRequest(ctx, http.MethodDelete, deleteUsersURL, request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error deleting users: %w", err)
	}

	return &response, annon, nil
}

// GetSigningGroups fetches a page of signing groups from the DocuSign account.
//
// Pagination: This endpoint supports cursor-based pagination using start_position and count parameters.
// The API returns up to 100 signing groups per page (controlled by PageOptions.PageSize).
// To fetch the next page, use the returned nextToken as PageOptions.PageToken.
//
// Returns: signing groups list, next page token (empty if last page), annotations, error.
func (c *Client) GetSigningGroups(ctx context.Context, options PageOptions) ([]SigningGroup, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var signingGroupsResponse SigningGroupResponse

	signingGroupsURL, err := c.prepareClientPagedRequest(getSigningGroups, options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, signingGroupsURL, nil, &signingGroupsResponse)
	if err != nil {
		return nil, "", nil, err
	}

	nextToken := getNextToken(signingGroupsResponse.Page)

	return signingGroupsResponse.SigningGroups, nextToken, annos, nil
}

// GetSigningGroupUsers fetches a page of users that belong to a specific signing group.
//
// Pagination: This endpoint supports cursor-based pagination using start_position and count parameters.
// The API returns up to 100 users per page (controlled by PageOptions.PageSize).
// To fetch the next page, use the returned nextToken as PageOptions.PageToken.
//
// Returns: users list, next page token (empty if last page), annotations, error.
func (c *Client) GetSigningGroupUsers(ctx context.Context, groupId string, options PageOptions) ([]User, string, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, "", nil, err
	}

	var groupMembersResponse UsersResponse

	signedGroupDetailsURL, err := c.prepareSigningGroupUsersRequest(groupId, options)
	if err != nil {
		return nil, "", nil, err
	}

	_, annos, err := c.doRequest(ctx, http.MethodGet, signedGroupDetailsURL, nil, &groupMembersResponse)
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

	userURL, err := c.buildClientURL(getUsers)
	if err != nil {
		return nil, nil, err
	}
	ApplyQueryParam(userURL, "status", "Active,ActivationSent")
	ApplyQueryParam(userURL, "email", userEmail)

	var usersResponse UsersResponse
	_, annos, err := c.doRequest(ctx, http.MethodGet, userURL, nil, &usersResponse)
	if err != nil {
		return nil, annos, fmt.Errorf("error fetching user details: %w", err)
	}

	if len(usersResponse.Users) != 1 {
		return nil, annos, fmt.Errorf("error fetching user details. Got %d users with the email %s", len(usersResponse.Users), userEmail)
	}
	user := usersResponse.Users[0]

	return &user, annos, nil
}

// GetPermissionProfiles fetches all permission profiles from the DocuSign account.
//
// Pagination: This endpoint does NOT support pagination. It returns all permission profiles in a single request.
// Typically, DocuSign accounts have a limited number of permission profiles (< 50), so this is acceptable.
//
// Returns: all permission profiles, annotations, error.
func (c *Client) GetPermissionProfiles(ctx context.Context) ([]PermissionProfile, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	var permissionProfilesResponse PermissionProfilesResponse

	baseURL, permissionProfilesURL, err := c.buildPermissionProfilesURL()
	if err != nil {
		return nil, nil, err
	}

	permissionProfilesURL = baseURL.ResolveReference(permissionProfilesURL)

	_, annos, err := c.doRequest(ctx, http.MethodGet, permissionProfilesURL, nil, &permissionProfilesResponse)
	if err != nil {
		return nil, nil, err
	}

	return permissionProfilesResponse.PermissionProfiles, annos, nil
}

// UpdateGroupUsers adds users to a group using the DocuSign API.
// Based on API: PUT /restapi/v2.1/accounts/{accountId}/groups/{groupId}/users.
func (c *Client) UpdateGroupUsers(ctx context.Context, groupID string, request GroupUsersRequest) (*GroupUsersResponse, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	if len(request.Users) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	updateGroupUsersURL, err := c.buildClientURL(updateGroupUsers, groupID)
	if err != nil {
		return nil, nil, err
	}

	var response GroupUsersResponse
	_, annon, err := c.doRequest(ctx, http.MethodPut, updateGroupUsersURL, request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error updating group users: %w", err)
	}

	return &response, annon, nil
}

// UpdateSigningGroup updates a signing group by adding users to it.
// Based on API: PUT /restapi/v2.1/accounts/{accountId}/signing_groups/{signingGroupId}.
func (c *Client) UpdateSigningGroup(ctx context.Context, signingGroupID string, request SigningGroupUsersRequest) (*SigningGroupUsersResponse, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	if len(request.Users) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	updateSigningGroupUsersURL, err := c.buildClientURL(updateSigningGroupUsers, signingGroupID)
	if err != nil {
		return nil, nil, err
	}

	var response SigningGroupUsersResponse
	_, annon, err := c.doRequest(ctx, http.MethodPut, updateSigningGroupUsersURL, request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error updating signing group: %w", err)
	}

	return &response, annon, nil
}

// UpdateUserProfile updates a user's permission profile.
// Based on API: PUT /restapi/v2.1/accounts/{accountId}/users/{userId}/profile.
func (c *Client) UpdateUserProfile(ctx context.Context, userID string, request UpdateUserProfileRequest) (annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	updateUserProfileURL, err := c.buildClientURL(updateUserProfile, userID)
	if err != nil {
		return nil, err
	}

	_, annon, err := c.doRequest(ctx, http.MethodPut, updateUserProfileURL, request, nil)
	if err != nil {
		return annon, fmt.Errorf("error updating user profile: %w", err)
	}

	return annon, nil
}

// DeleteGroupUsers removes users from a group using the DocuSign API.
// Based on API: DELETE /restapi/v2.1/accounts/{accountId}/groups/{groupId}/users.
func (c *Client) DeleteGroupUsers(ctx context.Context, groupID string, request GroupUsersRequest) (*GroupUsersResponse, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	if len(request.Users) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	deleteGroupUsersURL, err := c.buildClientURL(deleteGroupUsers, groupID)
	if err != nil {
		return nil, nil, err
	}

	var response GroupUsersResponse
	_, annon, err := c.doRequest(ctx, http.MethodDelete, deleteGroupUsersURL, request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error deleting group users: %w", err)
	}

	return &response, annon, nil
}

// DeleteSigningGroupUsers removes users from a signing group using the DocuSign API.
// Based on API: DELETE /restapi/v2.1/accounts/{accountId}/signing_groups/{signingGroupId}/users.
func (c *Client) DeleteSigningGroupUsers(ctx context.Context, signingGroupID string, request SigningGroupUsersRequest) (*SigningGroupUsersResponse, annotations.Annotations, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, nil, err
	}

	if len(request.Users) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	deleteSigningGroupUsersURL, err := c.buildClientURL(deleteSigningGroupUsers, signingGroupID)
	if err != nil {
		return nil, nil, err
	}

	var response SigningGroupUsersResponse
	_, annon, err := c.doRequest(ctx, http.MethodDelete, deleteSigningGroupUsersURL, request, &response)
	if err != nil {
		return nil, annon, fmt.Errorf("error deleting signing group users: %w", err)
	}

	return &response, annon, nil
}

// doRequest executes an HTTP request and decodes the response into the provided result.
// If body is nil, the request is sent without a body (useful for GET requests).
func (c *Client) doRequest(
	ctx context.Context,
	method string,
	url *url.URL,
	body any,
	response any,
) (http.Header, annotations.Annotations, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, nil, err
	}

	requestOptions := []uhttp.RequestOption{
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(token.AccessToken),
	}

	if body != nil {
		requestOptions = append(requestOptions, uhttp.WithJSONBody(body))
	}

	request, err := c.wrapper.NewRequest(ctx, method, url, requestOptions...)
	if err != nil {
		return nil, nil, err
	}

	return doRequestCommon(c.wrapper, request, response)
}
