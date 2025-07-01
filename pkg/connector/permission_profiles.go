package connector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

// permissionBuilder handles the construction of permission-related resources and grants.
type permissionBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

// ResourceType returns the resource type this builder manages (docusign-permissions).
func (p *permissionBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return permissionResourceType
}

// List returns the singleton permission resource that represents all DocuSign permissions.
func (p *permissionBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	permissionResource, err := resource.NewRoleResource(
		permissionResourceID,
		permissionResourceType,
		permissionResourceID,
		nil,
	)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to create permission resource: %w", err)
	}

	return []*v2.Resource{permissionResource}, "", annos, nil
}

// GetPermissionResource returns the singleton permission resource.
func (p *permissionBuilder) GetPermissionResource(ctx context.Context) (*v2.Resource, error) {
	permissionResource, err := resource.NewRoleResource(
		permissionResourceID,
		permissionResourceType,
		permissionResourceID,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create permission resource: %w", err)
	}
	return permissionResource, nil
}

// Entitlements generates all possible permission entitlements for the permission resource.
func (p *permissionBuilder) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	entitlements := make([]*v2.Entitlement, 0, len(permissionDefinitions))
	annos := annotations.Annotations{}
	for _, permission := range permissionDefinitions {
		entitlements = append(entitlements, entitlement.NewPermissionEntitlement(
			resource,
			permission.ID,
			entitlement.WithDisplayName(permission.DisplayName),
			entitlement.WithDescription(permission.Description),
			entitlement.WithGrantableTo(userResourceType),
		))
	}

	return entitlements, "", annos, nil
}

// Grants would assign permissions to users. This is intentionally left empty as grants are now handled by the userBuilder.
func (p *permissionBuilder) Grants(
	ctx context.Context,
	permissionResource *v2.Resource,
	pageToken *pagination.Token,
) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// makeUserSubjectID creates a ResourceId for a user based on their user ID.
func makeUserSubjectID(userID string) *v2.ResourceId {
	return &v2.ResourceId{
		ResourceType: userResourceType.Id,
		Resource:     userID,
	}
}

// parseUserSettings converts user settings interface into a map for easier processing.
func parseUserSettings(settings interface{}) (map[string]interface{}, error) {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal user settings: %w", err)
	}

	var settingsMap map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settingsMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user settings: %w", err)
	}

	return settingsMap, nil
}

// newPermissionBuilder creates a new permissionBuilder instance.
func newPermissionBuilder(client *client.Client) *permissionBuilder {
	return &permissionBuilder{
		resourceType: permissionResourceType,
		client:       client,
	}
}
