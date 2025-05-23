package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/test"
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

// Helper function to create a new client instance.
func createClient(baseURL string) *client.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: test.MockAccessToken})
	oauthClient := oauth2.NewClient(context.Background(), ts)
	baseHttpClient := uhttp.NewBaseHttpClient(oauthClient)
	return client.NewClient(context.Background(), baseURL, test.MockAccountID, ts, baseHttpClient)
}

// Test case to verify successful retrieval of users without pagination.
func TestClient_GetUsers(t *testing.T) {
	t.Run("successfully retrieves users without pagination", func(t *testing.T) {
		mockResponse := readMockResponse("users_list.json")
		testServer := createTestServer(t, mockResponse, getUsersTest, "")

		defer testServer.Close()

		c := createClient(testServer.URL)
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

		c := createClient(testServer.URL)
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

		c := createClient(testServer.URL)
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

		c := createClient(testServer.URL)
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

		c := createClient(testServer.URL)
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
