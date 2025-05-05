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
	createUsers    = "/restapi/v2.1/accounts/%s/users"
)

// Client wraps HTTP interactions with the DocuSign API, handling auth and base URL.
type Client struct {
	apiUrl    string
	token     string
	accountId string
	wrapper   *uhttp.BaseHttpClient
}

// New constructs a Client, choosing OAuth2 interactive flow or direct token based on accessToken.
func New(ctx context.Context, apiUrl, accountId, clientID, clientSecret, redirectURI, accessToken string) (*Client, error) {
	var (
		baseClient *uhttp.BaseHttpClient
		err        error
	)

	if accessToken != "" {
		baseClient, err = NewClientFromAccessToken(ctx, accessToken)
		if err != nil {
			return nil, err
		}
	} else {
		oauth := NewOAuth2Docusign(clientID, clientSecret, redirectURI)
		baseClient, err = oauth.Client(ctx)
		if err != nil {
			return nil, err
		}
		accessToken = oauth.Token().AccessToken
	}

	return &Client{
		apiUrl:    apiUrl,
		token:     accessToken,
		accountId: accountId,
		wrapper:   baseClient,
	}, nil
}

// NewClient initializes a Client with a fixed token and optional HTTP wrapper.
func NewClient(ctx context.Context, apiUrl, token, accountId string, httpClient ...*uhttp.BaseHttpClient) *Client {
	var wrapper *uhttp.BaseHttpClient
	if len(httpClient) > 0 {
		wrapper = httpClient[0]
	} else {
		client, err := NewClientFromAccessToken(ctx, token)
		if err != nil {
			wrapper = &uhttp.BaseHttpClient{}
		} else {
			wrapper = client
		}
	}

	return &Client{
		apiUrl:    apiUrl,
		token:     token,
		accountId: accountId,
		wrapper:   wrapper,
	}
}

// GetUsers fetches a page of users and returns users, next page token, and annotations.
func (c *Client) GetUsers(ctx context.Context, token string) ([]User, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	var usersResponse UsersResponse

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	endpoint := fmt.Sprintf(getUsers, c.accountId)
	usersEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid users endpoint: %w", err)
	}

	usersURL := baseURL.ResolveReference(usersEndpoint)

	pt, err := DecodePageToken(token)
	if err != nil {
		return nil, "", nil, err
	}

	q := usersURL.Query()
	q.Set("start_position", fmt.Sprintf("%d", pt.StartPosition))
	q.Set("count", "50")
	usersURL.RawQuery = q.Encode()

	headers, _, err := c.doRequest(ctx, http.MethodGet, usersURL.String(), &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	if desc, err := ratelimit.ExtractRateLimitData(http.StatusOK, &headers); err == nil {
		annos.WithRateLimiting(desc)
	}

	var nextToken string
	if usersResponse.Page.EndPosition < usersResponse.Page.TotalSetSize {
		nextStart := usersResponse.Page.EndPosition + 1
		nextToken = EncodePageToken(&pageToken{StartPosition: nextStart})
	}

	return usersResponse.Users, nextToken, annos, nil
}

// GetGroups fetches a page of groups and handles pagination and rate limit annotations.
func (c *Client) GetGroups(ctx context.Context, token string) ([]Group, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	var groupsResponse GroupsResponse

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	endpoint := fmt.Sprintf(getGroups, c.accountId)
	groupsEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid group endpoint: %w", err)
	}

	groupsURL := baseURL.ResolveReference(groupsEndpoint)

	pt, err := DecodePageToken(token)
	if err != nil {
		return nil, "", nil, err
	}

	q := groupsURL.Query()
	q.Set("start_position", fmt.Sprintf("%d", pt.StartPosition))
	q.Set("count", "50")
	groupsURL.RawQuery = q.Encode()

	headers, _, err := c.doRequest(ctx, http.MethodGet, groupsURL.String(), &groupsResponse)
	if err != nil {
		return nil, "", nil, err
	}

	if desc, err := ratelimit.ExtractRateLimitData(http.StatusOK, &headers); err == nil {
		annos.WithRateLimiting(desc)
	}

	var nextToken string
	if groupsResponse.Page.EndPosition < groupsResponse.Page.TotalSetSize {
		nextStart := groupsResponse.Page.EndPosition + 1
		nextToken = EncodePageToken(&pageToken{StartPosition: nextStart})
	}

	return groupsResponse.Groups, nextToken, annos, nil
}

// GetGroupUsers fetches users for a group with pagination support.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string, token string) ([]User, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	var usersResponse UsersResponse

	baseURL, err := url.Parse(c.apiUrl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid base URL: %w", err)
	}

	endpoint := fmt.Sprintf(getGroupUsers, c.accountId, groupId)
	groupUsersEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", nil, fmt.Errorf("invalid groupUser endpoint: %w", err)
	}

	groupUsersURL := baseURL.ResolveReference(groupUsersEndpoint)

	pt, err := DecodePageToken(token)
	if err != nil {
		return nil, "", nil, err
	}

	q := groupUsersURL.Query()
	q.Set("start_position", fmt.Sprintf("%d", pt.StartPosition))
	q.Set("count", "50")
	groupUsersURL.RawQuery = q.Encode()

	headers, _, err := c.doRequest(ctx, http.MethodGet, groupUsersURL.String(), &usersResponse)
	if err != nil {
		return nil, "", nil, err
	}

	if desc, err := ratelimit.ExtractRateLimitData(http.StatusOK, &headers); err == nil {
		annos.WithRateLimiting(desc)
	}

	var nextToken string
	if usersResponse.Page.EndPosition < usersResponse.Page.TotalSetSize {
		nextStart := usersResponse.Page.EndPosition + 1
		nextToken = EncodePageToken(&pageToken{StartPosition: nextStart})
	}

	return usersResponse.Users, nextToken, annos, nil
}

// GetUserDetails fetches detailed information for a specific user, including permissions.
func (c *Client) GetUserDetails(ctx context.Context, userID string) (*UserDetail, annotations.Annotations, error) {
	userURL, err := BuildURL(c.apiUrl, getPermissions, c.accountId, userID)
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

// GetAllUsersWithDetails retrieves every user and their permissions by paging through all users.
func (c *Client) GetAllUsersWithDetails(ctx context.Context) ([]*UserDetail, annotations.Annotations, error) {
	var allUserDetails []*UserDetail
	allAnnos := annotations.Annotations{}
	var nextToken string

	for {
		users, newToken, annos, err := c.GetUsers(ctx, nextToken)
		if err != nil {
			return nil, allAnnos, fmt.Errorf("error fetching users page: %w", err)
		}
		allAnnos = append(allAnnos, annos...)

		for _, user := range users {
			detail, detailAnnos, err := c.GetUserDetails(ctx, user.UserId)
			if err != nil {
				return nil, allAnnos, fmt.Errorf("error fetching user details for %s: %w", user.UserId, err)
			}
			allAnnos = append(allAnnos, detailAnnos...)
			allUserDetails = append(allUserDetails, detail)
		}

		if newToken == "" {
			break
		}
		nextToken = newToken
	}

	return allUserDetails, allAnnos, nil
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
	_, ann, err := c.doRequestWithBody(ctx, http.MethodPost, createUsersURL.String(), request, &response)
	if err != nil {
		return nil, ann, fmt.Errorf("error creating users: %w", err)
	}

	return &response, ann, nil
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

	return DoRequestCommon(c.wrapper, req, res)
}
