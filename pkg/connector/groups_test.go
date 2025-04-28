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
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

/* Reads a mock response from a file. */
func ReadMockResponse(filename string) string {
	data := test.ReadFile(filename)
	return data
}

/* Creates a fake HTTP response with a JSON body. */
func createMockResponseGroup(statusCode int, body string) *http.Response {
	resp := &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

/* Tests listing groups with different server responses. */
func TestGroupBuilder_List(t *testing.T) {
	mockGroup := ReadMockResponse("groups.json")
	mockGroupEmpty := ReadMockResponse("groupempty.json")
	mockApiError := ReadMockResponse("apierror.json")

	tests := []struct {
		name        string
		mockResp    *http.Response
		expectError bool
		expectedLen int
	}{
		{
			name:        "successful group list",
			mockResp:    createMockResponseGroup(http.StatusOK, mockGroup), //nolint:bodyclose // Body closes elsewhere
			expectError: false,
			expectedLen: 2,
		},
		{
			name:        "empty group list",
			mockResp:    createMockResponseGroup(http.StatusOK, mockGroupEmpty), //nolint:bodyclose // Body closes elsewhere
			expectError: false,
			expectedLen: 0,
		},
		{
			name:        "api error",
			mockResp:    createMockResponseGroup(http.StatusInternalServerError, mockApiError), //nolint:bodyclose // Body closes elsewhere
			expectError: true,
			expectedLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer tt.mockResp.Body.Close()

			testClient := test.NewTestClient(tt.mockResp, nil)
			builder := &groupBuilder{
				resourceType: groupResourceType,
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
				assert.Equal(t, "Admins", resources[0].DisplayName)
				assert.Equal(t, "1", resources[0].Id.Resource)
			}
		})
	}
}

/* Tests listing groups with a direct mock client. */
func TestGroupBuilder_List_WithMockClient(t *testing.T) {
	// Creamos un mockClient que implementa la interfaz completa de *client.Client
	mockClient := &struct {
		*test.MockClient
	}{
		MockClient: &test.MockClient{
			GetGroupsFunc: func(ctx context.Context) ([]client.Group, annotations.Annotations, error) {
				return []client.Group{
					{
						GroupId:    "1",
						GroupName:  "Admins",
						GroupType:  "adminGroup",
						UsersCount: "5",
					},
				}, nil, nil
			},
		},
	}

	builder := &groupBuilder{
		resourceType: groupResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resources, _, _, err := builder.List(ctx, nil, nil)

	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, "Admins", resources[0].DisplayName)
}

/* Tests generating entitlements ("member") for a group. */
func TestGroupBuilder_Entitlements(t *testing.T) {
	mockClient := &struct {
		*test.MockClient
	}{}

	builder := &groupBuilder{
		resourceType: groupResourceType,
		client:       mockClient,
	}

	groupResource, err := resource.NewGroupResource(
		"testgroup",
		groupResourceType,
		"123",
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()
	entitlements, _, _, err := builder.Entitlements(ctx, groupResource, nil)

	require.NoError(t, err)
	assert.Len(t, entitlements, 1)

	assert.Equal(t, "member", entitlements[0].Slug)
	assert.Equal(t, "Member of testgroup", entitlements[0].DisplayName)
	assert.Equal(t, "Member of testgroup group", entitlements[0].Description)
}

/* Tests generating user grants (membership link) for a group. */
func TestGroupBuilder_Grants(t *testing.T) {
	mockClient := &struct {
		*test.MockClient
	}{
		MockClient: &test.MockClient{
			GetGroupUsersFunc: func(ctx context.Context, groupID string) ([]client.User, annotations.Annotations, error) {
				return []client.User{
					{
						UserId:     "user1",
						UserName:   "testuser1",
						Email:      "user1@test.com",
						UserStatus: "active",
					},
				}, nil, nil
			},
		},
	}

	builder := &groupBuilder{
		resourceType: groupResourceType,
		client:       mockClient,
	}

	groupResource, err := resource.NewGroupResource(
		"testgroup",
		groupResourceType,
		"123",
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()
	grants, _, _, err := builder.Grants(ctx, groupResource, nil)

	require.NoError(t, err)
	assert.Len(t, grants, 1)
	assert.Equal(t, "user1", grants[0].Principal.Id.Resource)
	assert.Equal(t, "group:123:member", grants[0].Entitlement.Id)
}
