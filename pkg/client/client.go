package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/ratelimit"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
)

// API endpoint constants.
const (
	getUsers       = "/restapi/v2.1/accounts/%s/users"
	getGroups      = "/restapi/v2.1/accounts/%s/groups"
	getPermissions = "/restapi/v2.1/accounts/%s/users/%s"
	getGroupUsers  = "/restapi/v2.1/accounts/%s/groups/%s/users"
	getUserGroups  = "/restapi/v2.1/accounts/%s/users/%s"
	createUsers    = "/restapi/v2.1/accounts/%s/users"
)

// Client is a wrapper for making authenticated API requests to the DocuSign API.
type Client struct {
	apiUrl       string
	token        string
	accountId    string
	clientID     string
	clientSecret string
	refreshToken string
	wrapper      *uhttp.BaseHttpClient
}

// New creates a new Client instance, automatically performing OAuth2 authentication using client credentials and refresh token.
func New(ctx context.Context, input *Client) (*Client, error) {
	httpClient, token, err := NewAuthenticatedClient(ctx, input.clientID, input.clientSecret, input.accountId, input.apiUrl)
	if err != nil {
		return nil, err
	}

	baseHttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	return &Client{
		wrapper:      baseHttpClient,
		apiUrl:       input.apiUrl,
		token:        token.AccessToken,
		accountId:    input.accountId,
		clientID:     input.clientID,
		clientSecret: input.clientSecret,
		refreshToken: token.RefreshToken,
	}, nil
}

// NewClient creates a new Client instance using the provided token and optional HTTP client.
func NewClient(ctx context.Context, apiUrl string, token string, account string, clientId string, clientSecret string, refreshToken string, httpClient ...*uhttp.BaseHttpClient) *Client {
	var wrapper = &uhttp.BaseHttpClient{}
	if len(httpClient) > 0 {
		wrapper = httpClient[0]
	}

	return &Client{
		wrapper:      wrapper,
		apiUrl:       apiUrl,
		token:        token,
		accountId:    account,
		clientID:     clientId,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
	}
}

// GetUsers retrieves all users in the account, paginated.
func (c *Client) GetUsers(ctx context.Context) ([]User, annotations.Annotations, error) {
	var allUsers []User
	startPosition := 0
	annotationsOut := annotations.Annotations{}

	for {
		baseURL, err := url.Parse(c.apiUrl)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid base URL: %w", err)
		}

		usersPath := fmt.Sprintf(getUsers, c.accountId)
		usersEndpoint, err := url.Parse(usersPath)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid users endpoint: %w", err)
		}

		usersURL := baseURL.ResolveReference(usersEndpoint)
		query := usersURL.Query()
		query.Set("startPosition", fmt.Sprintf("%d", startPosition))
		usersURL.RawQuery = query.Encode()

		var response UsersResponse
		_, ann, err := c.doRequest(ctx, http.MethodGet, usersURL.String(), &response)
		if err != nil {
			return nil, ann, fmt.Errorf("error fetching users: %w", err)
		}

		allUsers = append(allUsers, response.Users...)
		annotationsOut = append(annotationsOut, ann...)

		if response.Page.EndPosition+1 >= response.Page.TotalSetSize {
			break
		}

		startPosition = response.Page.EndPosition + 1
	}

	return allUsers, annotationsOut, nil
}

// GetGroups retrieves all groups in the account, paginated.
func (c *Client) GetGroups(ctx context.Context) ([]Group, annotations.Annotations, error) {
	var allGroups []Group
	startPosition := 0
	annotationsOut := annotations.Annotations{}

	for {
		baseURL, err := url.Parse(c.apiUrl)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid base URL: %w", err)
		}

		groupsPath := fmt.Sprintf(getGroups, c.accountId)
		groupsEndpoint, err := url.Parse(groupsPath)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid groups endpoint: %w", err)
		}

		groupURL := baseURL.ResolveReference(groupsEndpoint)
		query := groupURL.Query()
		query.Set("startPosition", fmt.Sprintf("%d", startPosition))
		groupURL.RawQuery = query.Encode()

		var response GroupsResponse
		_, ann, err := c.doRequest(ctx, http.MethodGet, groupURL.String(), &response)
		if err != nil {
			return nil, ann, fmt.Errorf("error fetching groups: %w", err)
		}

		allGroups = append(allGroups, response.Groups...)
		annotationsOut = append(annotationsOut, ann...)

		if response.Page.EndPosition+1 >= response.Page.TotalSetSize {
			break
		}

		startPosition = response.Page.EndPosition + 1
	}

	return allGroups, annotationsOut, nil
}

// GetUserGroups retrieves all groups associated with a given user.
func (c *Client) GetUserGroups(ctx context.Context, userID string) ([]Group, annotations.Annotations, error) {
	var allGroups []Group
	startPosition := 0
	annotationsOut := annotations.Annotations{}

	for {
		baseURL, err := url.Parse(c.apiUrl)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid base URL: %w", err)
		}

		userGroupsPath := fmt.Sprintf(getUserGroups, c.accountId, userID)
		userGroupsEndpoint, err := url.Parse(userGroupsPath)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid user groups endpoint: %w", err)
		}

		userGroupURL := baseURL.ResolveReference(userGroupsEndpoint)
		query := userGroupURL.Query()
		query.Set("startPosition", fmt.Sprintf("%d", startPosition))
		userGroupURL.RawQuery = query.Encode()

		var response GroupsResponse
		_, ann, err := c.doRequest(ctx, http.MethodGet, userGroupURL.String(), &response)
		if err != nil {
			return nil, ann, fmt.Errorf("error fetching user groups: %w", err)
		}

		allGroups = append(allGroups, response.Groups...)
		annotationsOut = append(annotationsOut, ann...)

		if response.Page.EndPosition+1 >= response.Page.TotalSetSize {
			break
		}

		startPosition = response.Page.EndPosition + 1
	}

	return allGroups, annotationsOut, nil
}

// GetGroupUsers retrieves all users associated with a specific group.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string) ([]User, annotations.Annotations, error) {
	var allUsers []User
	startPosition := 0
	annotationsOut := annotations.Annotations{}

	for {
		baseURL, err := url.Parse(c.apiUrl)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid base URL: %w", err)
		}

		groupUsersPath := fmt.Sprintf(getGroupUsers, c.accountId, groupId)
		groupUsersEndpoint, err := url.Parse(groupUsersPath)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid group users endpoint: %w", err)
		}

		groupUsersURL := baseURL.ResolveReference(groupUsersEndpoint)
		query := groupUsersURL.Query()
		query.Set("startPosition", fmt.Sprintf("%d", startPosition))
		groupUsersURL.RawQuery = query.Encode()

		var response UsersResponse
		_, ann, err := c.doRequest(ctx, http.MethodGet, groupUsersURL.String(), &response)
		if err != nil {
			return nil, ann, fmt.Errorf("error fetching group users: %w", err)
		}

		allUsers = append(allUsers, response.Users...)
		annotationsOut = append(annotationsOut, ann...)

		if response.Page.EndPosition+1 >= response.Page.TotalSetSize {
			break
		}

		startPosition = response.Page.EndPosition + 1
	}

	return allUsers, annotationsOut, nil
}

// GetUserDetails retrieves details about a specific user, including permissions.
func (c *Client) GetUserDetails(ctx context.Context, userID string) (*UserDetail, annotations.Annotations, error) {
	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid base URL: %w", err)
	}

	userEndpointPath := fmt.Sprintf(getPermissions, c.accountId, userID)
	userEndpoint, err := url.Parse(userEndpointPath)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid user details endpoint: %w", err)
	}

	userURL := baseURL.ResolveReference(userEndpoint)

	var userDetail UserDetail
	_, ann, err := c.doRequest(ctx, http.MethodGet, userURL.String(), &userDetail)
	if err != nil {
		return nil, ann, fmt.Errorf("error fetching user details: %w", err)
	}

	return &userDetail, ann, nil
}

// GetAllUsersWithDetails retrieves all users and their corresponding detailed information.
func (c *Client) GetAllUsersWithDetails(ctx context.Context) ([]*UserDetail, annotations.Annotations, error) {
	users, annos, err := c.GetUsers(ctx)
	if err != nil {
		return nil, annos, err
	}

	var userDetails []*UserDetail
	for _, user := range users {
		detail, newAnnos, err := c.GetUserDetails(ctx, user.UserId)
		if err != nil {
			return nil, annos, err
		}

		annos = append(annos, newAnnos...)
		userDetails = append(userDetails, detail)
	}

	return userDetails, annos, nil
}

// CreateUsers creates one or more new users in the account.
func (c *Client) CreateUsers(ctx context.Context, request CreateUsersRequest) (*UserCreationResponse, annotations.Annotations, error) {
	if len(request.NewUsers) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid base URL: %w", err)
	}

	createUsersPath := fmt.Sprintf(createUsers, c.accountId)
	createUsersEndpoint, err := url.Parse(createUsersPath)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid create users endpoint: %w", err)
	}

	createUsersURL := baseURL.ResolveReference(createUsersEndpoint)

	var response UserCreationResponse
	_, ann, err := c.doRequestWithBody(ctx, http.MethodPost, createUsersURL.String(), request, &response)
	if err != nil {
		return nil, ann, fmt.Errorf("error creating users: %w", err)
	}

	return &response, ann, nil
}

// doRequestWithBody performs an HTTP request with a JSON body and decodes the response.
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

	req, err := c.wrapper.NewRequest(
		ctx,
		method,
		parsedURL,
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(c.token),
		uhttp.WithJSONBody(body),
	)
	if err != nil {
		return nil, nil, err
	}

	var resp *http.Response
	var doOptions []uhttp.DoOption
	if res != nil {
		doOptions = append(doOptions, uhttp.WithJSONResponse(res))
	}

	resp, err = c.wrapper.Do(req, doOptions...)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	annotation := annotations.Annotations{}
	if desc, err := ratelimit.ExtractRateLimitData(resp.StatusCode, &resp.Header); err == nil {
		annotation.WithRateLimiting(desc)
	}

	return resp.Header, annotation, nil
}

// doRequest performs an HTTP request without a body and decodes the response if provided.
func (c *Client) doRequest(
	ctx context.Context,
	method string,
	requestURL string,
	res interface{},
) (http.Header, annotations.Annotations, error) {
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return nil, nil, err
	}

	req, err := c.wrapper.NewRequest(
		ctx,
		method,
		parsedURL,
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(c.token),
	)
	if err != nil {
		return nil, nil, err
	}

	var resp *http.Response
	var doOptions []uhttp.DoOption
	if res != nil {
		doOptions = append(doOptions, uhttp.WithJSONResponse(res))
	}

	resp, err = c.wrapper.Do(req, doOptions...)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	annotation := annotations.Annotations{}
	if desc, err := ratelimit.ExtractRateLimitData(resp.StatusCode, &resp.Header); err == nil {
		annotation.WithRateLimiting(desc)
	}

	return resp.Header, annotation, nil
}
