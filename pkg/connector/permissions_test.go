package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPermissionBuilder_List tests the List method of permissionBuilder.
// Verifies that it returns exactly one permission resource with correct properties.
// and no pagination token or annotations.
func TestPermissionBuilder_List(t *testing.T) {
	mockClient := &client.Client{}

	builder := &permissionBuilder{
		resourceType: permissionResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resources, nextToken, annos, err := builder.List(ctx, nil, nil)

	require.NoError(t, err)
	require.Len(t, resources, 1)

	resource := resources[0]
	assert.Equal(t, permissionResourceID, resource.Id.Resource)
	assert.Equal(t, permissionResourceType.Id, resource.Id.ResourceType)
	assert.Empty(t, nextToken)
	assert.NotNil(t, annos)
	assert.NotEmpty(t, resource.DisplayName)
}

// TestPermissionBuilder_GetPermissionResource tests the GetPermissionResource method.
// Verifies it returns a properly formatted permission resource with correct ID and type.
// and a non-empty display name.
func TestPermissionBuilder_GetPermissionResource(t *testing.T) {
	mockClient := &client.Client{}

	builder := &permissionBuilder{
		resourceType: permissionResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resource, err := builder.GetPermissionResource(ctx)

	require.NoError(t, err)
	require.NotNil(t, resource)

	assert.Equal(t, permissionResourceID, resource.Id.Resource)
	assert.Equal(t, permissionResourceType.Id, resource.Id.ResourceType)
	assert.NotEmpty(t, resource.DisplayName)
}

// TestPermissionBuilder_EntitlementsAndGrants tests both Entitlements and Grants methods.
// Verifies that for permission resources:.
// - No entitlements are returned.
// - No grants are returned.
// - Proper empty responses are provided for tokens and annotations.
func TestPermissionBuilder_EntitlementsAndGrants(t *testing.T) {
	mockClient := &client.Client{}
	builder := &permissionBuilder{
		resourceType: permissionResourceType,
		client:       mockClient,
	}

	ctx := context.Background()
	resource, _ := builder.GetPermissionResource(ctx)

	// Test Entitlements.
	entitlements, nextEntToken, entAnnos, entErr := builder.Entitlements(ctx, resource, nil)
	assert.NoError(t, entErr)
	assert.NotEmpty(t, entitlements, "Entitlements should not be empty since permissionDefinitions is populated")
	assert.Empty(t, nextEntToken)
	assert.NotNil(t, entAnnos)

	// Validar que cada entitlement tenga los campos esperados
	for _, e := range entitlements {
		assert.NotEmpty(t, e.Id, "Entitlement ID should not be empty")
		assert.NotEmpty(t, e.DisplayName, "Entitlement DisplayName should not be empty")
		assert.Equal(t, resource.Id.ResourceType, e.Resource.Id.ResourceType, "ResourceType should match")
	}

	// Test Grants.
	grants, nextGrantToken, grantAnnos, grantErr := builder.Grants(ctx, resource, nil)
	assert.NoError(t, grantErr)
	assert.Empty(t, grants)
	assert.Empty(t, nextGrantToken)
	assert.Nil(t, grantAnnos)
}
