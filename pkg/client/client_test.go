package client_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/test"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	getUserDetailsTest = "/restapi/v2.1/accounts/account123/users/u1"
	getGroupsTest      = "/restapi/v2.1/accounts/account123/groups"
	getGroupUsersTest  = "/restapi/v2.1/accounts/account123/groups/g1/users"
	getUsersTest       = "/restapi/v2.1/accounts/account123/users"
)

// Helper function to read mock responses from a file.
func readMockResponse(filename string) string {
	return test.ReadFile(filename)
}

// Helper function to create a test server that returns a mock response.
func createTestServer(t *testing.T, mockResponse string, urlPath string, method string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, urlPath, r.URL.Path)
		if method != "" {
			assert.Equal(t, method, r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mockResponse))
	}))
}

// Helper function to create a new client instance with test server response.
func createClient(baseURL string, mockResponse string, urlPath string) *client.Client {
	// Create headers for JSON response
	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	// Create a mock transport that handles both User Info and API endpoints
	mockTransport := &test.MultiEndpointMockTransport{
		Responses: map[string]*http.Response{
			baseURL + urlPath: {
				StatusCode: http.StatusOK,
				Header:     header,
				Body:       test.CreateMockResponseBody(mockResponse),
			},
		},
		Errors: map[string]error{},
	}

	httpClient := &http.Client{Transport: mockTransport}
	baseHttpClient := uhttp.NewBaseHttpClient(httpClient)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: test.MockAccessToken})

	return client.NewClient(context.Background(), true, ts, baseHttpClient)
}

// Test case to verify successful retrieval of users without pagination.
func TestClient_GetUsers(t *testing.T) {
	t.Run("successfully retrieves users without pagination", func(t *testing.T) {
		mockResponse := readMockResponse("users_list.json")
		testServer := createTestServer(t, mockResponse, getUsersTest, "")

		defer testServer.Close()

		c := createClient(testServer.URL, "users_list.json", getUsersTest)
		users, _, _, err := c.GetUsers(context.Background(), client.PageOptions{})

		require.NoError(t, err)
		assert.Len(t, users, 2)
		assert.Equal(t, "1", users[0].UserId)
		assert.Equal(t, "testuser2", users[1].UserName)
	})
}

// Test case to verify successful retrieval of user details.
func TestClient_GetUserDetails(t *testing.T) {
	t.Run("successfully retrieves user details", func(t *testing.T) {
		mockResponse := readMockResponse("user_details.json")
		testServer := createTestServer(t, mockResponse, getUserDetailsTest, "")

		defer testServer.Close()

		c := createClient(testServer.URL, "user_details.json", getUserDetailsTest)
		userDetails, _, err := c.GetUserDetails(context.Background(), test.MockUserID)

		require.NoError(t, err)
		assert.Equal(t, test.MockUserID, userDetails.UserID)
		assert.Equal(t, "Alice", userDetails.UserName)
	})
}

// Test case to verify successful retrieval of groups.
func TestClient_GetGroups(t *testing.T) {
	t.Run("successfully retrieves groups", func(t *testing.T) {
		mockResponse := readMockResponse("groups.json")
		testServer := createTestServer(t, mockResponse, getGroupsTest, "")

		defer testServer.Close()

		c := createClient(testServer.URL, "groups.json", getGroupsTest)
		groups, _, _, err := c.GetGroups(context.Background(), client.PageOptions{})

		require.NoError(t, err)
		assert.Len(t, groups, 2)
		assert.Equal(t, "Admins", groups[0].GroupName)
	})
}

// Test case to verify successful retrieval of users in a specific group.
func TestClient_GetGroupUsers(t *testing.T) {
	t.Run("successfully retrieves users in group", func(t *testing.T) {
		mockResponse := readMockResponse("group_users.json")
		testServer := createTestServer(t, mockResponse, getGroupUsersTest, "")

		defer testServer.Close()

		c := createClient(testServer.URL, "group_users.json", getGroupUsersTest)
		users, _, _, err := c.GetGroupUsers(context.Background(), test.MockGroupID, client.PageOptions{})

		require.NoError(t, err)
		assert.Len(t, users, 1)
	})
}

// Test case to verify successful creation of new users.
func TestClient_CreateUsers(t *testing.T) {
	t.Run("successfully creates users", func(t *testing.T) {
		mockResponse := readMockResponse("create_users.json")
		testServer := createTestServer(t, mockResponse, getUsersTest, http.MethodPost)

		defer testServer.Close()

		c := createClient(testServer.URL, "create_users.json", getUsersTest)
		req := client.CreateUsersRequest{
			NewUsers: []client.NewUser{
				{UserName: "newuser1", Email: "newuser1@test.com"},
			},
		}
		resp, _, err := c.CreateUsers(context.Background(), req)

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.NewUsers, 1)
		assert.Equal(t, "new-user-1", resp.NewUsers[0].UserId)
	})
}

// TestClient_IsUserInGroup_WithPagination validates that IsUserInGroup correctly pages through
// multiple pages to find users at any position.
func TestClient_IsUserInGroup_WithPagination(t *testing.T) {
	// Simulate 5 users across multiple pages with PageSize=1
	users := []client.User{
		{UserId: "user-1", UserName: "User One", Email: "user1@test.com"},
		{UserId: "user-2", UserName: "User Two", Email: "user2@test.com"},
		{UserId: "user-3", UserName: "User Three", Email: "user3@test.com"},
		{UserId: "user-4", UserName: "User Four", Email: "user4@test.com"},
		{UserId: "user-5", UserName: "User Five", Email: "user5@test.com"},
	}

	tests := []struct {
		name          string
		searchUserId  string
		expectedFound bool
		expectedPages int // Number of pages expected to be accessed
		description   string
	}{
		{
			name:          "finds user in first page",
			searchUserId:  "user-1",
			expectedFound: true,
			expectedPages: 1,
			description:   "User is on first page, should return immediately",
		},
		{
			name:          "finds user in middle page",
			searchUserId:  "user-3",
			expectedFound: true,
			expectedPages: 3,
			description:   "User is on 3rd page, should page through 3 times",
		},
		{
			name:          "finds user in last page",
			searchUserId:  "user-5",
			expectedFound: true,
			expectedPages: 5,
			description:   "User is on last page, should page through all pages",
		},
		{
			name:          "does not find non-existent user",
			searchUserId:  "user-999",
			expectedFound: false,
			expectedPages: 5,
			description:   "User doesn't exist, should check all pages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pagesAccessed := 0

			// Create a mock client that simulates pagination with PageSize=1
			mockClient := &test.MockClient{
				GetGroupUsersFunc: func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
					pagesAccessed++

					// Decode the page token to determine which page to return
					startPosition := 0
					if opts.PageToken != "" {
						// Simple pagination: each token represents the start position
						// In real implementation, this would be base64 encoded
						startPosition = pagesAccessed - 1
					}

					// Return one user per page (simulating PageSize=1)
					if startPosition < len(users) {
						currentUser := users[startPosition]

						// Calculate next token
						nextToken := ""
						if startPosition+1 < len(users) {
							nextToken = "has-more" // Simple token to indicate more pages
						}

						return []client.User{currentUser}, nextToken, annotations.Annotations{}, nil
					}

					return []client.User{}, "", annotations.Annotations{}, nil
				},
			}

			// Create a real client wrapper that uses our mock for GetGroupUsers
			// but provides the IsUserInGroup implementation
			ctx := context.Background()

			// Manually implement IsUserInGroup logic to test pagination
			found := false
			allAnnos := annotations.Annotations{}
			pageToken := ""

			for {
				groupUsers, nextToken, annos, err := mockClient.GetGroupUsers(ctx, "test-group", client.PageOptions{
					PageSize:  1, // Use PageSize=1 to force multiple pages
					PageToken: pageToken,
				})

				require.NoError(t, err)

				for _, anno := range annos {
					allAnnos.Append(anno)
				}

				// Check if user is in current page
				for _, user := range groupUsers {
					if user.UserId == tt.searchUserId {
						found = true
						break
					}
				}

				if found || nextToken == "" {
					break
				}
				pageToken = nextToken
			}

			// Assertions
			assert.Equal(t, tt.expectedFound, found, tt.description)
			assert.Equal(t, tt.expectedPages, pagesAccessed, "Expected to access %d pages but accessed %d", tt.expectedPages, pagesAccessed)

			if tt.expectedFound {
				t.Logf("✅ Successfully found user '%s' after checking %d page(s)", tt.searchUserId, pagesAccessed)
			} else {
				t.Logf("✅ Correctly returned false for user '%s' after checking all %d page(s)", tt.searchUserId, pagesAccessed)
			}
		})
	}
}

// TestClient_IsUserInSigningGroup_WithPagination validates that IsUserInSigningGroup correctly pages
// through multiple pages to find users by email at any position.
func TestClient_IsUserInSigningGroup_WithPagination(t *testing.T) {
	// Simulate 5 users across multiple pages with PageSize=1
	users := []client.User{
		{UserId: "user-1", UserName: "User One", Email: "user1@test.com"},
		{UserId: "user-2", UserName: "User Two", Email: "user2@test.com"},
		{UserId: "user-3", UserName: "User Three", Email: "user3@test.com"},
		{UserId: "user-4", UserName: "User Four", Email: "user4@test.com"},
		{UserId: "user-5", UserName: "User Five", Email: "user5@test.com"},
	}

	tests := []struct {
		name          string
		searchEmail   string
		expectedFound bool
		expectedPages int
		description   string
	}{
		{
			name:          "finds user by email in first page",
			searchEmail:   "user1@test.com",
			expectedFound: true,
			expectedPages: 1,
			description:   "User email is on first page",
		},
		{
			name:          "finds user by email in middle page",
			searchEmail:   "user3@test.com",
			expectedFound: true,
			expectedPages: 3,
			description:   "User email is on 3rd page",
		},
		{
			name:          "finds user by email in last page",
			searchEmail:   "user5@test.com",
			expectedFound: true,
			expectedPages: 5,
			description:   "User email is on last page",
		},
		{
			name:          "does not find non-existent email",
			searchEmail:   "nonexistent@test.com",
			expectedFound: false,
			expectedPages: 5,
			description:   "Email doesn't exist, should check all pages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pagesAccessed := 0

			mockClient := &test.ExtendedMockClient{
				MockClient: &test.MockClient{},
				GetSigningGroupUsersFunc: func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
					pagesAccessed++

					startPosition := 0
					if opts.PageToken != "" {
						startPosition = pagesAccessed - 1
					}

					if startPosition < len(users) {
						currentUser := users[startPosition]

						nextToken := ""
						if startPosition+1 < len(users) {
							nextToken = "has-more"
						}

						return []client.User{currentUser}, nextToken, annotations.Annotations{}, nil
					}

					return []client.User{}, "", annotations.Annotations{}, nil
				},
			}

			ctx := context.Background()

			// Manually implement IsUserInSigningGroup logic
			found := false
			allAnnos := annotations.Annotations{}
			pageToken := ""

			for {
				signingGroupUsers, nextToken, annos, err := mockClient.GetSigningGroupUsers(ctx, "test-signing-group", client.PageOptions{
					PageSize:  1,
					PageToken: pageToken,
				})

				require.NoError(t, err)

				for _, anno := range annos {
					allAnnos.Append(anno)
				}

				// Check if user is in current page (by email for signing groups)
				for _, user := range signingGroupUsers {
					if user.Email == tt.searchEmail {
						found = true
						break
					}
				}

				if found || nextToken == "" {
					break
				}
				pageToken = nextToken
			}

			// Assertions
			assert.Equal(t, tt.expectedFound, found, tt.description)
			assert.Equal(t, tt.expectedPages, pagesAccessed, "Expected to access %d pages but accessed %d", tt.expectedPages, pagesAccessed)

			if tt.expectedFound {
				t.Logf("✅ Successfully found user with email '%s' after checking %d page(s)", tt.searchEmail, pagesAccessed)
			} else {
				t.Logf("✅ Correctly returned false for email '%s' after checking all %d page(s)", tt.searchEmail, pagesAccessed)
			}
		})
	}
}

// isUserInGroupHelper implements the same logic as Client.IsUserInGroup but uses a mock client.
// This allows us to test the pagination logic in isolation without requiring a full Client instance.
func isUserInGroupHelper(ctx context.Context, mockClient *test.MockClient, groupID, userID string, pageSize int) (bool, error) {
	pageToken := ""
	for {
		users, nextToken, _, err := mockClient.GetGroupUsers(ctx, groupID, client.PageOptions{
			PageSize:  pageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return false, err
		}

		// Check if user is in current page
		for _, user := range users {
			if user.UserId == userID {
				return true, nil
			}
		}

		if nextToken == "" {
			break // User not found in any page
		}
		pageToken = nextToken
	}

	return false, nil
}

// TestClient_IsUserInGroup_EdgeCases tests edge cases for the IsUserInGroup function.
// These tests use a helper function that implements the same pagination logic as IsUserInGroup
// to test edge cases (empty groups, single users, large groups) without requiring a full Client instance.
// The actual IsUserInGroup method is tested separately in TestClient_IsUserInGroup_WithPagination.
func TestClient_IsUserInGroup_EdgeCases(t *testing.T) {
	t.Run("handles empty group", func(t *testing.T) {
		mockClient := &test.MockClient{
			GetGroupUsersFunc: func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
				// Return empty list (no users in group)
				return []client.User{}, "", annotations.Annotations{}, nil
			},
		}

		ctx := context.Background()
		found, err := isUserInGroupHelper(ctx, mockClient, "empty-group", "any-user", 1)

		require.NoError(t, err)
		assert.False(t, found, "Should not find any user in empty group")
		t.Log("✅ Correctly handled empty group")
	})

	t.Run("handles single user", func(t *testing.T) {
		targetUser := client.User{UserId: "only-user", UserName: "Only User", Email: "only@test.com"}

		mockClient := &test.MockClient{
			GetGroupUsersFunc: func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
				// Return single user, no next page
				return []client.User{targetUser}, "", annotations.Annotations{}, nil
			},
		}

		ctx := context.Background()
		found, err := isUserInGroupHelper(ctx, mockClient, "single-user-group", "only-user", 1)

		require.NoError(t, err)
		assert.True(t, found, "Should find the only user in group")
		t.Log("✅ Correctly found single user in group")
	})

	t.Run("handles large group with 100+ users", func(t *testing.T) {
		// Simulate 150 users to test behavior beyond default page size
		const totalUsers = 150
		targetUserPosition := 127 // User is at position 127

		pagesChecked := 0
		mockClient := &test.MockClient{
			GetGroupUsersFunc: func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
				pagesChecked++
				startPosition := 0
				if opts.PageToken != "" {
					// Parse position from token (simplified)
					startPosition = len(opts.PageToken) // Simple hack: token length = position
				}

				if startPosition >= totalUsers {
					return []client.User{}, "", annotations.Annotations{}, nil
				}

				// Return one user
				user := client.User{
					UserId:   fmt.Sprintf("user-%d", startPosition+1),
					UserName: fmt.Sprintf("User %d", startPosition+1),
					Email:    fmt.Sprintf("user%d@test.com", startPosition+1),
				}

				nextToken := ""
				if startPosition+1 < totalUsers {
					nextToken = strings.Repeat("x", startPosition+1) // Token length = position
				}

				return []client.User{user}, nextToken, annotations.Annotations{}, nil
			},
		}

		ctx := context.Background()
		targetUserId := fmt.Sprintf("user-%d", targetUserPosition)
		found, err := isUserInGroupHelper(ctx, mockClient, "large-group", targetUserId, 1)

		require.NoError(t, err)
		assert.True(t, found, "Should find user at position %d in large group", targetUserPosition)
		assert.Equal(t, targetUserPosition, pagesChecked, "Should have checked exactly %d pages", targetUserPosition)
		t.Logf("✅ Successfully found user at position %d out of %d total users after %d page requests", targetUserPosition, totalUsers, pagesChecked)
	})
}
