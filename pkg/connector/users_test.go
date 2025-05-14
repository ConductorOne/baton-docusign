package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClient implements the UserClient interface with the minimum necessary.
type mockClient struct {
	getUsersFunc       func(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	getUserDetailsFunc func(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error)
}

func (m *mockClient) GetUsers(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
	if m.getUsersFunc != nil {
		return m.getUsersFunc(ctx, opts)
	}
	return nil, "", nil, errors.New("not implemented")
}

func (m *mockClient) GetUserDetails(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error) {
	if m.getUserDetailsFunc != nil {
		return m.getUserDetailsFunc(ctx, userID)
	}
	return nil, nil, errors.New("not implemented")
}

func (m *mockClient) CreateUsers(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error) {
	return nil, nil, errors.New("not implemented")
}

// TestUserBuilder_List tests the List method of userBuilder with different scenarios:.
// - When users exist in the response.
// - When the user list is empty.
func TestUserBuilder_List(t *testing.T) {
	tests := []struct {
		name        string
		mockFile    string
		expectError bool
		expectEmpty bool
	}{
		{"user list with results", "users_list.json", false, false},
		{"empty user list", "users_empty.json", false, true},
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

			mockClient := &mockClient{
				getUsersFunc: func(ctx context.Context, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
					return parsed.Users, parsed.NextPageToken, nil, nil
				},
			}

			builder := &userBuilder{
				resourceType: userResourceType,
				client:       mockClient,
			}

			ctx := context.Background()
			resources, _, _, err := builder.List(ctx, nil, &pagination.Token{})

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tt.expectEmpty {
				assert.Empty(t, resources)
			} else {
				assert.NotEmpty(t, resources)
			}
		})
	}
}

// TestParseIntoUserResource tests the conversion of a client.User object into a v2.Resource.
// Verifies that the conversion maintains the user ID and basic properties.
func TestParseIntoUserResource(t *testing.T) {
	tests := []struct {
		name    string
		user    *client.User
		wantErr bool
	}{
		{
			name: "active user",
			user: &client.User{
				UserId:     "u1",
				UserName:   "test",
				Email:      "test@example.com",
				UserStatus: "Active",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIntoUserResource(tt.user)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.user.UserId, got.Id.Resource)
		})
	}
}

// TestUserBuilder_Grants tests the Grants method of userBuilder.
// Verifies that grants are properly created based on user settings.
func TestUserBuilder_Grants(t *testing.T) {
	mockClient := &mockClient{
		getUserDetailsFunc: func(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error) {
			return &client.UserDetail{
				UserID: userID,
				UserSettings: client.UserSettings{
					CanManageAccount: "true",
				},
			}, nil, nil
		},
	}

	builder := &userBuilder{
		resourceType: userResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	userRes := &v2.Resource{
		Id: &v2.ResourceId{
			ResourceType: userResourceType.Id,
			Resource:     "test-user",
		},
	}

	grants, _, _, err := builder.Grants(ctx, userRes, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, grants)
}
