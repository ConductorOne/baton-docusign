package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/test"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock function to create mock group response.
func mockGroupResponse(groups []client.Group, nextPageToken string) *test.MockClient {
	return &test.MockClient{
		GetGroupsFunc: func(ctx context.Context, opts client.PageOptions) ([]client.Group, string, annotations.Annotations, error) {
			return groups, nextPageToken, annotations.Annotations{}, nil
		},
	}
}

// Reads a mock response from a file.
func ReadMockResponse(filename string) string {
	return test.ReadFile(filename)
}

// TestGroupBuilder_List tests listing groups for various scenarios from a JSON file.
func TestGroupBuilder_List(t *testing.T) {
	tests := []struct {
		name        string
		mockFile    string
		expectError bool
		expectedLen int
	}{
		{name: "successful group list", mockFile: "groups.json", expectError: false, expectedLen: 2},
		{name: "empty group list", mockFile: "groupempty.json", expectError: false, expectedLen: 0},
	}

	type groupsResponse struct {
		Groups        []client.Group `json:"groups"`
		NextPageToken string         `json:"nextPageToken"`
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := ReadMockResponse(tt.mockFile)

			var parsed groupsResponse
			require.NoError(t, json.NewDecoder(bytes.NewReader([]byte(data))).Decode(&parsed))

			mockClient := mockGroupResponse(parsed.Groups, parsed.NextPageToken)
			builder := newTestGroupBuilder(mockClient)
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

// TestGroupBuilder_List_WithMockClient tests listing groups using a predefined mock client.
func TestGroupBuilder_List_WithMockClient(t *testing.T) {
	mockClient := mockGroupResponse([]client.Group{{
		GroupId:    "1",
		GroupName:  "Admins",
		GroupType:  "adminGroup",
		UsersCount: "5",
	}}, "next_token")

	builder := newTestGroupBuilder(mockClient)
	ctx := context.Background()

	resources, serializedToken, annos, err := builder.List(ctx, nil, pageToken)
	require.NoError(t, err)

	assert.Len(t, resources, 1)
	assert.Equal(t, "Admins", resources[0].DisplayName)
	assert.Equal(t, "1", resources[0].Id.Resource)

	_, rawNext, parseErr := parsePageToken(serializedToken, &v2.ResourceId{ResourceType: groupResourceType.Id})
	require.NoError(t, parseErr)
	assert.Equal(t, "next_token", rawNext)

	assert.NotNil(t, annos)
}

// TestGroupBuilder_Pagination tests pagination behavior across multiple pages of groups.
func TestGroupBuilder_Pagination(t *testing.T) {
	mockClient := &test.MockClient{
		GetGroupsFunc: func(ctx context.Context, opts client.PageOptions) ([]client.Group, string, annotations.Annotations, error) {
			switch opts.PageToken {
			case "":
				return []client.Group{{GroupId: "1", GroupName: "Group 1"}}, "page-2", annotations.Annotations{}, nil
			case "page-2":
				return []client.Group{{GroupId: "2", GroupName: "Group 2"}}, "", annotations.Annotations{}, nil
			default:
				return nil, "", nil, fmt.Errorf("unexpected page token: %s", opts.PageToken)
			}
		},
	}

	builder := newTestGroupBuilder(mockClient)
	ctx := context.Background()

	resources, serializedToken, _, err := builder.List(ctx, nil, pageToken)
	require.NoError(t, err)
	assert.Len(t, resources, 1)

	_, rawNext, parseErr := parsePageToken(serializedToken, &v2.ResourceId{ResourceType: groupResourceType.Id})
	require.NoError(t, parseErr)
	assert.Equal(t, "page-2", rawNext)

	pToken2 := &pagination.Token{Size: 50, Token: serializedToken}
	resources2, serializedToken2, _, err2 := builder.List(ctx, nil, pToken2)
	require.NoError(t, err2)
	assert.Len(t, resources2, 1)

	_, rawNext2, parseErr2 := parsePageToken(serializedToken2, &v2.ResourceId{ResourceType: groupResourceType.Id})
	require.NoError(t, parseErr2)
	assert.Empty(t, rawNext2)
}

// TestGroupBuilder_Entitlements tests generation of entitlements for a group resource.
func TestGroupBuilder_Entitlements(t *testing.T) {
	mockClient := &test.MockClient{}

	builder := newTestGroupBuilder(mockClient)
	groupResource, err := resource.NewGroupResource(
		"testgroup",
		groupResourceType,
		"123",
		[]resource.GroupTraitOption{resource.WithGroupProfile(map[string]interface{}{"group_name": "testgroup"})},
	)
	require.NoError(t, err)
	ctx := context.Background()

	ents, nextToken, annos, err := builder.Entitlements(ctx, groupResource, pToken)
	require.NoError(t, err)
	assert.Len(t, ents, 1)
	assert.Empty(t, nextToken)
	assert.NotNil(t, annos)

	assert.Equal(t, "member", ents[0].Slug)
	assert.Equal(t, "Member of testgroup", ents[0].DisplayName)
	assert.Equal(t, "Member of testgroup group", ents[0].Description)
	assert.Equal(t, groupResourceType.Id, ents[0].Resource.Id.ResourceType)
	assert.Equal(t, "123", ents[0].Resource.Id.Resource)
}

// TestGroupBuilder_Grants tests retrieval of grants (users) for a group resource.
func TestGroupBuilder_Grants(t *testing.T) {
	mockClient := &test.MockClient{
		GetGroupUsersFunc: func(ctx context.Context, groupID string, opts client.PageOptions) ([]client.User, string, annotations.Annotations, error) {
			return []client.User{{
				UserId:     "user1",
				UserName:   "testuser1",
				Email:      "user1@test.com",
				UserStatus: "active",
			}}, "next_token", annotations.Annotations{}, nil
		},
	}

	builder := newTestGroupBuilder(mockClient)
	groupResource, err := resource.NewGroupResource(
		"testgroup",
		groupResourceType,
		"123",
		[]resource.GroupTraitOption{
			resource.WithGroupProfile(map[string]interface{}{"group_name": "testgroup"}),
		},
	)
	require.NoError(t, err)
	ctx := context.Background()

	grants, serializedToken, annos, err := builder.Grants(ctx, groupResource, pageToken)
	require.NoError(t, err)
	assert.Len(t, grants, 1)

	_, rawNext, parseErr := parsePageToken(serializedToken, &v2.ResourceId{ResourceType: userResourceType.Id})
	require.NoError(t, parseErr)
	assert.Equal(t, "next_token", rawNext)
	assert.NotNil(t, annos)
	assert.Equal(t, "user1", grants[0].Principal.Id.Resource)
	assert.Equal(t, groupResource.Id.Resource, grants[0].Entitlement.Resource.Id.Resource)
}

func newTestGroupBuilder(client groupsClientInterface) *groupBuilder {
	return &groupBuilder{
		resourceType: &v2.ResourceType{
			Id:          "group",
			DisplayName: "Group",
			Description: "A DocuSign group",
			Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_GROUP},
		},
		client: client,
	}
}
