package test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

const (
	MockAccountID    = "account123"
	MockBaseURL      = "https://mock.api.docusign.net"
	MockAccessToken  = "test-token"
	MockRefreshToken = "test-refresh-token"
	MockUserID       = "u1"
	MockGroupID      = "g1"
)

// MockRoundTripper is a mock implementation of http.RoundTripper for testing.
type MockRoundTripper struct {
	Response      *http.Response
	Err           error
	roundTripFunc func(*http.Request) (*http.Response, error)
}

// RoundTrip executes the mock RoundTripper function or returns the stored response and error.
func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.roundTripFunc != nil {
		return m.roundTripFunc(req)
	}
	return m.Response, m.Err
}

// MockClient is a mock client used for unit tests that simulates the real client behavior.
type MockClient struct {
	GetUsersFunc                func(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	GetGroupsFunc               func(ctx context.Context, opts client.PageOptions) ([]client.Group, string, annotations.Annotations, error)
	GetGroupUsersFunc           func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	IsUserInGroupFunc           func(ctx context.Context, groupID, userID string) (bool, annotations.Annotations, error)
	CreateUsersFunc             func(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error)
	UpdateGroupUsersFunc        func(ctx context.Context, groupID string, request client.UpdateGroupUsersRequest) (*client.UpdateGroupUsersResponse, annotations.Annotations, error)
	UpdateSigningGroupFunc      func(ctx context.Context, signingGroupID string, request client.UpdateSigningGroupRequest) (*client.UpdateSigningGroupResponse, annotations.Annotations, error)
	UpdateUserProfileFunc       func(ctx context.Context, userID string, request client.UpdateUserProfileRequest) (annotations.Annotations, error)
	DeleteGroupUsersFunc        func(ctx context.Context, groupID string, request client.UpdateGroupUsersRequest) (*client.UpdateGroupUsersResponse, annotations.Annotations, error)
	DeleteSigningGroupUsersFunc func(ctx context.Context, signingGroupID string, request client.UpdateSigningGroupRequest) (*client.UpdateSigningGroupResponse, annotations.Annotations, error)
	GetUserDetailsFunc          func(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error)
	GetUserByEmailFunc          func(ctx context.Context, userEmail string) (*client.User, annotations.Annotations, error)
}

// ExtendedMockClient is an extended version of MockClient with additional functionality for user details.
type ExtendedMockClient struct {
	*MockClient
	GetAllUsersWithDetailsFunc func(ctx context.Context) ([]*client.UserDetail, annotations.Annotations, error)
	GetSigningGroupsFunc       func(ctx context.Context, opts client.PageOptions) ([]client.SigningGroup, string, annotations.Annotations, error)
	GetSigningGroupUsersFunc   func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	IsUserInSigningGroupFunc   func(ctx context.Context, groupID, userEmail string) (bool, annotations.Annotations, error)
	GetPermissionProfilesFunc  func(ctx context.Context) ([]client.PermissionProfile, annotations.Annotations, error)
}

// GetUsers returns a list of users based on the mocked function.
func (m *MockClient) GetUsers(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
	if m.GetUsersFunc != nil {
		return m.GetUsersFunc(ctx, opts)
	}
	return nil, "", nil, nil
}

// GetGroups returns a list of groups based on the mocked function.
func (m *MockClient) GetGroups(ctx context.Context, opts client.PageOptions) ([]client.Group, string, annotations.Annotations, error) {
	if m.GetGroupsFunc != nil {
		return m.GetGroupsFunc(ctx, opts)
	}
	return nil, "", nil, nil
}

// GetGroupUsers returns a list of users for a given group based on the mocked function.
func (m *MockClient) GetGroupUsers(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
	if m.GetGroupUsersFunc != nil {
		return m.GetGroupUsersFunc(ctx, groupID, opts)
	}
	return nil, "", nil, nil
}

// IsUserInGroup checks if a user is in a group based on the mocked function.
func (m *MockClient) IsUserInGroup(ctx context.Context, groupID, userID string) (bool, annotations.Annotations, error) {
	if m.IsUserInGroupFunc != nil {
		return m.IsUserInGroupFunc(ctx, groupID, userID)
	}
	return false, nil, nil
}

// CreateUsers creates users based on the mocked function.
func (m *MockClient) CreateUsers(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error) {
	if m.CreateUsersFunc != nil {
		return m.CreateUsersFunc(ctx, request)
	}
	return nil, nil, nil
}

// UpdateGroupUsers updates group users based on the mocked function.
func (m *MockClient) UpdateGroupUsers(ctx context.Context, groupID string, request client.UpdateGroupUsersRequest) (*client.UpdateGroupUsersResponse, annotations.Annotations, error) {
	if m.UpdateGroupUsersFunc != nil {
		return m.UpdateGroupUsersFunc(ctx, groupID, request)
	}
	return nil, nil, nil
}

// UpdateSigningGroup updates a signing group based on the mocked function.
func (m *MockClient) UpdateSigningGroup(ctx context.Context, signingGroupID string, request client.UpdateSigningGroupRequest) (*client.UpdateSigningGroupResponse, annotations.Annotations, error) {
	if m.UpdateSigningGroupFunc != nil {
		return m.UpdateSigningGroupFunc(ctx, signingGroupID, request)
	}
	return nil, nil, nil
}

// UpdateUserProfile updates a user's profile based on the mocked function.
func (m *MockClient) UpdateUserProfile(ctx context.Context, userID string, request client.UpdateUserProfileRequest) (annotations.Annotations, error) {
	if m.UpdateUserProfileFunc != nil {
		return m.UpdateUserProfileFunc(ctx, userID, request)
	}
	return nil, nil
}

func (m *MockClient) DeleteGroupUsers(ctx context.Context, groupID string, request client.UpdateGroupUsersRequest) (*client.UpdateGroupUsersResponse, annotations.Annotations, error) {
	if m.DeleteGroupUsersFunc != nil {
		return m.DeleteGroupUsersFunc(ctx, groupID, request)
	}
	return nil, nil, nil
}

func (m *MockClient) DeleteSigningGroupUsers(
	ctx context.Context,
	signingGroupID string,
	request client.UpdateSigningGroupRequest,
) (*client.UpdateSigningGroupResponse, annotations.Annotations, error) {
	if m.DeleteSigningGroupUsersFunc != nil {
		return m.DeleteSigningGroupUsersFunc(ctx, signingGroupID, request)
	}
	return nil, nil, nil
}

// GetUserDetails returns user details based on the mocked function.
func (m *MockClient) GetUserDetails(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error) {
	if m.GetUserDetailsFunc != nil {
		return m.GetUserDetailsFunc(ctx, userID)
	}
	return nil, nil, nil
}

// GetUserByEmail returns a user by email based on the mocked function.
func (m *MockClient) GetUserByEmail(ctx context.Context, userEmail string) (*client.User, annotations.Annotations, error) {
	if m.GetUserByEmailFunc != nil {
		return m.GetUserByEmailFunc(ctx, userEmail)
	}
	return nil, nil, nil
}

// GetAllUsersWithDetails returns user details for all users, based on the mocked function.
func (m *ExtendedMockClient) GetAllUsersWithDetails(ctx context.Context) ([]*client.UserDetail, annotations.Annotations, error) {
	if m.GetAllUsersWithDetailsFunc != nil {
		return m.GetAllUsersWithDetailsFunc(ctx)
	}
	return nil, nil, nil
}

// GetSigningGroups returns a list of signing groups based on the mocked function.
func (m *ExtendedMockClient) GetSigningGroups(ctx context.Context, opts client.PageOptions) ([]client.SigningGroup, string, annotations.Annotations, error) {
	if m.GetSigningGroupsFunc != nil {
		return m.GetSigningGroupsFunc(ctx, opts)
	}
	return nil, "", nil, nil
}

// GetSigningGroupUsers returns a list of users for a given signing group based on the mocked function.
func (m *ExtendedMockClient) GetSigningGroupUsers(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
	if m.GetSigningGroupUsersFunc != nil {
		return m.GetSigningGroupUsersFunc(ctx, groupID, opts)
	}
	return nil, "", nil, nil
}

// IsUserInSigningGroup checks if a user is in a signing group based on the mocked function.
func (m *ExtendedMockClient) IsUserInSigningGroup(ctx context.Context, groupID, userEmail string) (bool, annotations.Annotations, error) {
	if m.IsUserInSigningGroupFunc != nil {
		return m.IsUserInSigningGroupFunc(ctx, groupID, userEmail)
	}
	return false, nil, nil
}

// GetPermissionProfiles returns a list of permission profiles based on the mocked function.
func (m *ExtendedMockClient) GetPermissionProfiles(ctx context.Context) ([]client.PermissionProfile, annotations.Annotations, error) {
	if m.GetPermissionProfilesFunc != nil {
		return m.GetPermissionProfilesFunc(ctx)
	}
	return nil, nil, nil
}

// CreateMockResponse creates a mock HTTP response with a status and mock response body.
func CreateMockResponse(fileName string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       CreateMockResponseBody(fileName),
	}
}

// CreateMockResponseBody creates a mock response body by reading a file.
func CreateMockResponseBody(fileName string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(ReadFile(fileName)))
}

// ReadFile reads the content of a file from the "mock_responses" folder.
func ReadFile(fileName string) string {
	_, filename, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(filename)
	fullPath := filepath.Join(baseDir, "mock_responses", fileName)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// MultiEndpointMockTransport handles multiple endpoints with different responses.
type MultiEndpointMockTransport struct {
	Responses map[string]*http.Response
	Errors    map[string]error
}

func (m *MultiEndpointMockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqURL := req.URL.String()

	// Check for User Info endpoint first
	if strings.Contains(reqURL, "account-d.docusign.com/oauth/userinfo") || strings.Contains(reqURL, "account.docusign.com/oauth/userinfo") {
		return createUserInfoResponse(), nil
	}

	// For other endpoints, match by path only since baseURL in test varies
	requestPath := req.URL.Path
	for responseURL, resp := range m.Responses {
		parsedURL, err := url.Parse(responseURL)
		if err != nil {
			continue
		}
		if parsedURL.Path == requestPath {
			return resp, m.Errors[responseURL]
		}
	}

	// Default fallback with proper headers
	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(`{"error": "not found"}`)),
	}, nil
}

func createUserInfoResponse() *http.Response {
	userInfoJSON := `{
		"sub": "test-user-id",
		"name": "Test User",
		"email": "test@example.com",
		"accounts": [
			{
				"account_id": "` + MockAccountID + `",
				"is_default": true,
				"account_name": "Test Account",
				"base_uri": "` + MockBaseURL + `",
				"organization_id": "test-org-id"
			}
		]
	}`

	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(userInfoJSON)),
	}
}

// NewTestClient prepares a Client pointing to a mock endpoint.
func NewTestClient(response *http.Response, err error) *client.Client {
	mockTransport := &MultiEndpointMockTransport{
		Responses: map[string]*http.Response{},
		Errors:    map[string]error{},
	}

	httpClient := &http.Client{Transport: mockTransport}
	baseHttpClient := uhttp.NewBaseHttpClient(httpClient)
	staticTokenSource := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken:  MockAccessToken,
		RefreshToken: MockRefreshToken,
	})
	return client.NewClient(
		context.Background(),
		true, // isDemo
		staticTokenSource,
		baseHttpClient,
	)
}
