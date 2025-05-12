package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/ratelimit"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// API endpoint constants.
const (
	getUsers       = "/restapi/v2.1/accounts/%s/users"
	getGroups      = "/restapi/v2.1/accounts/%s/groups"
	getPermissions = "/restapi/v2.1/accounts/%s/users/%s"
	getGroupUsers  = "/restapi/v2.1/accounts/%s/groups/%s/users"
	createUsers    = "/restapi/v2.1/accounts/%s/users"
)

// Client wraps HTTP interactions with the DocuSign API, handling auth and base URL.
type Client struct {
	apiUrl      string
	tokenSource oauth2.TokenSource
	accountId   string
	wrapper     *uhttp.BaseHttpClient
}

// New constructs a Client, choosing OAuth2 interactive flow or direct token based on accessToken.
func New(ctx context.Context, apiUrl, accountId, clientID, clientSecret, redirectURI, refreshToken string) (*Client, error) {
	tokenSource := getTokenSource(ctx, clientID, clientSecret, redirectURI, refreshToken)
	baseClient := oauth2.NewClient(ctx, tokenSource)

	return &Client{
		apiUrl:      apiUrl,
		tokenSource: tokenSource,
		accountId:   accountId,
		wrapper:     uhttp.NewBaseHttpClient(baseClient),
	}, nil
}

// NewClient initializes a Client with a fixed token and optional HTTP wrapper.
func NewClient(ctx context.Context, apiUrl, accountId string, tokenSource oauth2.TokenSource, httpClient ...*uhttp.BaseHttpClient) *Client {
	var wrapper *uhttp.BaseHttpClient
	if len(httpClient) > 0 {
		wrapper = httpClient[0]
	} else {
		baseClient := oauth2.NewClient(ctx, tokenSource)
		wrapper = uhttp.NewBaseHttpClient(baseClient)
	}

	return &Client{
		apiUrl:      apiUrl,
		tokenSource: tokenSource,
		accountId:   accountId,
		wrapper:     wrapper,
	}
}

// GetUsers fetches a page of users and returns users, next page token, and annotations.
func (c *Client) GetUsers(ctx context.Context, options PageOptions) ([]User, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	var usersResponse UsersResponse

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	usersURL, err := PreparePagedRequest(baseURL, fmt.Sprintf(getUsers, c.accountId), options)
	if err != nil {
		return nil, "", nil, err
	}

	headers, _, err := c.doRequest(ctx, http.MethodGet, usersURL.String(), &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	if desc, err := ratelimit.ExtractRateLimitData(http.StatusOK, &headers); err == nil {
		annos.WithRateLimiting(desc)
	}

	nextToken := GetNextToken(usersResponse.Page)
	return usersResponse.Users, nextToken, annos, nil
}

// GetGroups fetches a page of groups and handles pagination and rate limit annotations.
func (c *Client) GetGroups(ctx context.Context, options PageOptions) ([]Group, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	var groupsResponse GroupsResponse

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	groupsURL, err := PreparePagedRequest(baseURL, fmt.Sprintf(getGroups, c.accountId), options)
	if err != nil {
		return nil, "", nil, err
	}

	headers, _, err := c.doRequest(ctx, http.MethodGet, groupsURL.String(), &groupsResponse)
	if err != nil {
		return nil, "", nil, err
	}

	if desc, err := ratelimit.ExtractRateLimitData(http.StatusOK, &headers); err == nil {
		annos.WithRateLimiting(desc)
	}

	nextToken := GetNextToken(groupsResponse.Page)
	return groupsResponse.Groups, nextToken, annos, nil
}

// GetGroupUsers fetches users for a group with pagination support.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string, options PageOptions) ([]User, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	var usersResponse UsersResponse

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	groupUsersURL, err := PreparePagedRequest(baseURL, fmt.Sprintf(getGroupUsers, c.accountId, groupId), options)
	if err != nil {
		return nil, "", nil, err
	}

	headers, _, err := c.doRequest(ctx, http.MethodGet, groupUsersURL.String(), &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	if desc, err := ratelimit.ExtractRateLimitData(http.StatusOK, &headers); err == nil {
		annos.WithRateLimiting(desc)
	}

	nextToken := GetNextToken(usersResponse.Page)
	return usersResponse.Users, nextToken, annos, nil
}

// GetUserDetails fetches detailed information for a specific user, including permissions.
func (c *Client) GetUserDetails(ctx context.Context, userID string) (*UserDetail, annotations.Annotations, error) {
	userURL, err := BuildURL(c.apiUrl, getPermissions, c.accountId, userID)
	if err != nil {
		return nil, nil, err
	}

	var userDetail UserDetail
	_, annos, err := c.doRequest(ctx, http.MethodGet, userURL.String(), &userDetail)
	if err != nil {
		return nil, annos, fmt.Errorf("error fetching user details: %w", err)
	}

	return &userDetail, annos, nil
}

// GetAllUsersWithDetails retrieves every user and their permissions by paging through all users.
func (c *Client) GetAllUsersWithDetails(
	ctx context.Context,
) ([]*UserDetail, annotations.Annotations, error) {
	var allUserDetails []*UserDetail
	annos := annotations.Annotations{}
	nextToken := ""

	for {
		opts := PageOptions{
			PageSize:  DefaultPageSize,
			PageToken: nextToken,
		}
		users, newToken, newAnnos, err := c.GetUsers(ctx, opts)
		if err != nil {
			return nil, annos, fmt.Errorf("error fetching users page: %w", err)
		}
		for _, annon := range newAnnos {
			annos.Append(annon)
		}
		for _, u := range users {
			detail, detailAnnos, err := c.GetUserDetails(ctx, u.UserId)
			if err != nil {
				return nil, annos, fmt.Errorf("error fetching user details for %s: %w", u.UserId, err)
			}
			for _, annon := range detailAnnos {
				annos.Append(annon)
			}
			allUserDetails = append(allUserDetails, detail)
		}
		if newToken == "" {
			break
		}
		nextToken = newToken
	}

	return allUserDetails, annos, nil
}

// CreateUsers sends a bulk create request for new users in the account.
func (c *Client) CreateUsers(ctx context.Context, request CreateUsersRequest) (*UserCreationResponse, annotations.Annotations, error) {
	if len(request.NewUsers) == 0 {
		return nil, nil, fmt.Errorf("at least one user must be provided")
	}

	createUsersURL, err := BuildURL(c.apiUrl, createUsers, c.accountId)
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

	return DoRequestCommon(c.wrapper, req, res)
}

// doRequest builds and executes an HTTP request without a body, decoding JSON response if provided.
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
	)
	if err != nil {
		return nil, nil, err
	}

	return DoRequestCommon(c.wrapper, req, res)
}
