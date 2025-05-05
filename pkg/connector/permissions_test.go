package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/test"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createUserDetail simplifies the creation of a UserDetail with the specified attributes.
func createUserDetail(userId, userName, email, isAdmin, userStatus, permissionProfileName string, userSettings client.UserSettings) *client.UserDetail {
	return &client.UserDetail{
		UserID:                userId,
		UserName:              userName,
		Email:                 email,
		IsAdmin:               isAdmin,
		UserStatus:            userStatus,
		PermissionProfileName: permissionProfileName,
		UserSettings:          userSettings,
	}
}

// TestPermissionBuilder_List verifies that the permission builder correctly lists the permission resource.
func TestPermissionBuilder_List(t *testing.T) {
	builder := &permissionBuilder{
		resourceType: permissionResourceType,
		client:       test.NewTestClient(nil, nil),
	}

	ctx := context.Background()
	resources, nextToken, annos, err := builder.List(ctx, nil, nil)

	require.NoError(t, err)
	assert.Len(t, resources, 1)
	assert.Equal(t, permissionResourceID, resources[0].DisplayName)
	assert.Equal(t, "", nextToken)
	assert.Nil(t, annos)
}

// TestPermissionBuilder_Entitlements verifies that the permission builder returns the correct entitlements for a resource.
func TestPermissionBuilder_Entitlements(t *testing.T) {
	builder := &permissionBuilder{
		resourceType: permissionResourceType,
		client:       test.NewTestClient(nil, nil),
	}

	ctx := context.Background()
	resource := &v2.Resource{
		Id: &v2.ResourceId{
			ResourceType: "docusign-permissions",
			Resource:     "docusign-permissions",
		},
		DisplayName: "DocuSign Permissions",
	}

	entitlements, _, _, err := builder.Entitlements(ctx, resource, nil)

	require.NoError(t, err)
	assert.Len(t, entitlements, len(permissionDefinitions))

	for _, e := range entitlements {
		assert.NotEmpty(t, e.Slug)
		assert.NotEmpty(t, e.DisplayName)
		assert.Equal(t, "docusign-permissions", e.Resource.Id.Resource)
	}
}

// TestPermissionBuilder_Grants verifies that the permissionBuilder correctly generates grants.
func TestPermissionBuilder_Grants(t *testing.T) {
	mockClient := &test.ExtendedMockClient{
		MockClient: &test.MockClient{},
		GetAllUsersWithDetailsFunc: func(ctx context.Context) ([]*client.UserDetail, annotations.Annotations, error) {
			return []*client.UserDetail{
				createUserDetail("user-1", "Alice", "alice@example.com", "true", "active", "Admin", client.UserSettings{
					CanManageAccount: "true",
					EnableVaulting:   "true",
					AdminOnly:        "true",
				}),
			}, nil, nil
		},
	}

	builder := &permissionBuilder{
		resourceType: permissionResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resources, _, _, err := builder.List(ctx, nil, nil)
	require.NoError(t, err)

	grants, _, _, err := builder.Grants(ctx, resources[0], nil)
	require.NoError(t, err)

	assert.Len(t, grants, 3)

	expectedEntitlements := map[string]bool{
		"adminOnly":        true,
		"canManageAccount": true,
		"enableVaulting":   true,
	}

	for _, g := range grants {
		assert.Equal(t, "user-1", g.Principal.Id.Resource)
		expectedEntitlements[g.Entitlement.Slug] = true
	}

	for slug, granted := range expectedEntitlements {
		assert.True(t, granted, "Expected grant for entitlement %s", slug)
	}
}
