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
	redirectUri  string
	wrapper      *uhttp.BaseHttpClient
}

// New creates a new Client instance, automatically performing OAuth2 authentication.
func New(ctx context.Context, apiUrl string, account string, clientId string, clientSecret string, redirectUri string) (*Client, error) {
	httpClient, token, err := NewAuthenticatedClient(ctx, clientId, clientSecret, account, redirectUri)
	if err != nil {
		return nil, err
	}

	baseHttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	return &Client{
		wrapper:      baseHttpClient,
		apiUrl:       apiUrl,
		token:        token.AccessToken,
		accountId:    account,
		clientID:     clientId,
		clientSecret: clientSecret,
		redirectUri:  redirectUri,
	}, nil
}

// NewClient creates a new Client instance using the provided token.
func NewClient(ctx context.Context, apiUrl string, token string, account string, clientId string, clientSecret string, redirectURI string, httpClient ...*uhttp.BaseHttpClient) *Client {
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
		redirectUri:  redirectURI,
	}
}

// buildURL constructs a complete URL from base path and path parameters
func (c *Client) buildURL(path string, pathParams ...interface{}) (*url.URL, error) {
	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	formattedPath := fmt.Sprintf(path, pathParams...)
	endpoint, err := url.Parse(formattedPath)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint path: %w", err)
	}

	return baseURL.ResolveReference(endpoint), nil
}

// paginatedRequest handles paginated requests for a given endpoint
func (c *Client) paginatedRequest(
	ctx context.Context,
	path string,
	pathParams []interface{},
	response interface{},
	extractItems func() []interface{},
) ([]interface{}, annotations.Annotations, error) {
	var allItems []interface{}
	var annotationsOut annotations.Annotations
	startPosition := 0

	for {
		url, err := c.buildURL(path, pathParams...)
		if err != nil {
			return nil, nil, err
		}

		query := url.Query()
		query.Set("startPosition", fmt.Sprintf("%d", startPosition))
		url.RawQuery = query.Encode()

		_, ann, err := c.doRequest(ctx, http.MethodGet, url.String(), response)
		if err != nil {
			return nil, annotationsOut, err
		}

		allItems = append(allItems, extractItems()...)
		annotationsOut = append(annotationsOut, ann...)

		page := getPageFromResponse(response)
		if page.EndPosition+1 >= page.TotalSetSize {
			break
		}

		startPosition = page.EndPosition + 1
	}

	return allItems, annotationsOut, nil
}

// GetUsers retrieves all users in the account, paginated.
func (c *Client) GetUsers(ctx context.Context) ([]User, annotations.Annotations, error) {
	var response UsersResponse
	items, ann, err := c.paginatedRequest(
		ctx,
		getUsers,
		[]interface{}{c.accountId},
		&response,
		func() []interface{} {
			var items []interface{}
			for _, user := range response.Users {
				items = append(items, user)
			}
			return items
		},
	)

	if err != nil {
		return nil, ann, fmt.Errorf("error fetching users: %w", err)
	}

	var users []User
	for _, item := range items {
		users = append(users, item.(User))
	}

	return users, ann, nil
}

// GetGroups retrieves all groups in the account, paginated.
func (c *Client) GetGroups(ctx context.Context) ([]Group, annotations.Annotations, error) {
	var response GroupsResponse
	items, ann, err := c.paginatedRequest(
		ctx,
		getGroups,
		[]interface{}{c.accountId},
		&response,
		func() []interface{} {
			var items []interface{}
			for _, group := range response.Groups {
				items = append(items, group)
			}
			return items
		},
	)

	if err != nil {
		return nil, ann, fmt.Errorf("error fetching groups: %w", err)
	}

	var groups []Group
	for _, item := range items {
		groups = append(groups, item.(Group))
	}

	return groups, ann, nil
}

// GetUserGroups retrieves all groups associated with a given user.
func (c *Client) GetUserGroups(ctx context.Context, userID string) ([]Group, annotations.Annotations, error) {
	var response GroupsResponse
	items, ann, err := c.paginatedRequest(
		ctx,
		getUserGroups,
		[]interface{}{c.accountId, userID},
		&response,
		func() []interface{} {
			var items []interface{}
			for _, group := range response.Groups {
				items = append(items, group)
			}
			return items
		},
	)

	if err != nil {
		return nil, ann, fmt.Errorf("error fetching user groups: %w", err)
	}

	var groups []Group
	for _, item := range items {
		groups = append(groups, item.(Group))
	}

	return groups, ann, nil
}

// GetGroupUsers retrieves all users associated with a specific group.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string) ([]User, annotations.Annotations, error) {
	var response UsersResponse
	items, ann, err := c.paginatedRequest(
		ctx,
		getGroupUsers,
		[]interface{}{c.accountId, groupId},
		&response,
		func() []interface{} {
			var items []interface{}
			for _, user := range response.Users {
				items = append(items, user)
			}
			return items
		},
	)

	if err != nil {
		return nil, ann, fmt.Errorf("error fetching group users: %w", err)
	}

	var users []User
	for _, item := range items {
		users = append(users, item.(User))
	}

	return users, ann, nil
}

// GetUserDetails retrieves details about a specific user, including permissions.
func (c *Client) GetUserDetails(ctx context.Context, userID string) (*UserDetail, annotations.Annotations, error) {
	userURL, err := c.buildURL(getPermissions, c.accountId, userID)
	if err != nil {
		return nil, nil, err
	}

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

	createUsersURL, err := c.buildURL(createUsers, c.accountId)
	if err != nil {
		return nil, nil, err
	}

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

	return c.doRequestCommon(req, res)
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

	return c.doRequestCommon(req, res)
}

// doRequestCommon handles common request processing logic
func (c *Client) doRequestCommon(req *http.Request, res interface{}) (http.Header, annotations.Annotations, error) {
	var doOptions []uhttp.DoOption
	if res != nil {
		doOptions = append(doOptions, uhttp.WithJSONResponse(res))
	}

	resp, err := c.wrapper.Do(req, doOptions...)
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

// Helper interface to extract page information from different response types
type pagedResponse interface {
	GetPage() Page
}

func (r *UsersResponse) GetPage() Page {
	return r.Page
}

func (r *GroupsResponse) GetPage() Page {
	return r.Page
}

// Helper function to get page info from response
func getPageFromResponse(response interface{}) Page {
	if r, ok := response.(pagedResponse); ok {
		return r.GetPage()
	}

	return Page{}
}
