package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/test"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockUserResponse creates a MockClient with GetUsersFunc using PageOptions.
func mockUserResponse(users []client.User, nextPageToken string) *test.MockClient {
	return &test.MockClient{
		GetUsersFunc: func(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
			return users, nextPageToken, annotations.Annotations{}, nil
		},
	}
}

// TestUserBuilder_List tests the List method of userBuilder using a mocked HTTP client response.
// It verifies that users are correctly parsed from the API response without errors.
func TestUserBuilder_List(t *testing.T) {
	tests := []struct {
		name        string
		mockFile    string
		expectError bool
		expectedLen int
	}{
		{
			name:        "successful user list",
			mockFile:    "users_list.json",
			expectError: false,
			expectedLen: 2,
		},
		{
			name:        "empty user list",
			mockFile:    "users_empty.json",
			expectError: false,
			expectedLen: 0,
		},
	}

	type usersResponse struct {
		Users         []client.User `json:"users"`
		NextPageToken string        `json:"nextPageToken"`
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := ReadMockResponse(tt.mockFile)

			var parsed usersResponse
			require.NoError(t, json.NewDecoder(bytes.NewReader([]byte(data))).Decode(&parsed))

			mockClient := mockUserResponse(parsed.Users, parsed.NextPageToken)
			builder := &userBuilder{
				resourceType: userResourceType,
				client:       mockClient,
			}

			ctx := context.Background()
			resources, serializedToken, annos, err := builder.List(ctx, nil, pageToken)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Len(t, resources, tt.expectedLen)
			assert.NotNil(t, annos)

			if parsed.NextPageToken != "" {
				_, rawNext, parseErr := parsePageToken(serializedToken, &v2.ResourceId{ResourceType: groupResourceType.Id})
				require.NoError(t, parseErr)
				assert.Equal(t, parsed.NextPageToken, rawNext)
			} else {
				assert.Empty(t, serializedToken)
			}

			if tt.expectedLen > 0 {
				assert.NotEmpty(t, resources[0].DisplayName)
				assert.NotEmpty(t, resources[0].Id.Resource)
			}
		})
	}
}

// TestUserBuilder_List_WithMockClient tests the List method of userBuilder using a mock client interface.
// It verifies that the List method can correctly retrieve and parse a manually mocked user list.
func TestUserBuilder_List_WithMockClient(t *testing.T) {
	mockClient := mockUserResponse([]client.User{
		{
			UserId:     "1",
			UserName:   "testuser1",
			Email:      "user1@test.com",
			UserStatus: "Active",
		},
	}, "")

	builder := &userBuilder{
		resourceType: userResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resources, nextToken, annos, err := builder.List(ctx, nil, pageToken)

	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Empty(t, nextToken)
	assert.NotNil(t, annos)
	assert.Equal(t, "testuser1", resources[0].DisplayName)
}
