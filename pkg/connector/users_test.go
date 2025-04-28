package connector

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/test"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createMockResponse creates a mock HTTP response with the given status code and body.
// Used to simulate API responses during testing.
func createMockResponse(statusCode int, body string) *http.Response {
	resp := &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

// TestUserBuilder_List tests the List method of userBuilder using a mocked HTTP client response.
// It verifies that users are correctly parsed from the API response without errors.
func TestUserBuilder_List(t *testing.T) {
	mockUsersList := ReadMockResponse("users_list.json")
	tests := []struct {
		name        string
		mockResp    *http.Response
		expectError bool
		expectedLen int
	}{
		{
			name:        "successful user list",
			mockResp:    createMockResponse(http.StatusOK, mockUsersList), //nolint:bodyclose // Body closes elsewhere
			expectError: false,
			expectedLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := test.NewTestClient(tt.mockResp, nil)
			builder := &userBuilder{
				resourceType: userResourceType,
				client:       testClient,
			}

			ctx := context.Background()
			resources, nextToken, annos, err := builder.List(ctx, nil, nil)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Len(t, resources, tt.expectedLen)
			assert.Empty(t, nextToken)
			assert.NotNil(t, annos)

			if tt.expectedLen > 0 {
				assert.Equal(t, "testuser1", resources[0].DisplayName)
			}
		})
	}
}

// TestUserBuilder_List_WithMockClient tests the List method of userBuilder using a mock client interface.
// It verifies that the List method can correctly retrieve and parse a manually mocked user list.
func TestUserBuilder_List_WithMockClient(t *testing.T) {
	mockClient := &test.MockClient{
		GetUsersFunc: func(ctx context.Context) ([]client.User, annotations.Annotations, error) {
			return []client.User{
				{
					UserId:     "1",
					UserName:   "testuser1",
					Email:      "user1@test.com",
					UserStatus: "Active",
				},
			}, nil, nil
		},
	}

	builder := &userBuilder{
		resourceType: userResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resources, _, _, err := builder.List(ctx, nil, nil)

	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, "testuser1", resources[0].DisplayName)
}
