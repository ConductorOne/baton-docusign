package test

import (
	"context"
	"io"
	"net/http"
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
	GetUsersFunc      func(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	GetGroupsFunc     func(ctx context.Context, opts client.PageOptions) ([]client.Group, string, annotations.Annotations, error)
	GetGroupUsersFunc func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	CreateUsersFunc   func(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error)
}

// ExtendedMockClient is an extended version of MockClient with additional functionality for user details.
type ExtendedMockClient struct {
	*MockClient
	GetAllUsersWithDetailsFunc func(ctx context.Context) ([]*client.UserDetail, annotations.Annotations, error)
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

// CreateUsers creates users based on the mocked function.
func (m *MockClient) CreateUsers(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error) {
	if m.CreateUsersFunc != nil {
		return m.CreateUsersFunc(ctx, request)
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

// NewTestClient prepares a Client pointing to a mock endpoint.
func NewTestClient(response *http.Response, err error) *client.Client {
	mockTransport := &MockRoundTripper{Response: response, Err: err}
	httpClient := &http.Client{Transport: mockTransport}
	baseHttpClient := uhttp.NewBaseHttpClient(httpClient)
	staticTokenSource := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken:  MockAccessToken,
		RefreshToken: MockRefreshToken,
	})
	return client.NewClient(
		context.Background(),
		MockBaseURL,
		MockAccountID,
		staticTokenSource,
		baseHttpClient,
	)
}
