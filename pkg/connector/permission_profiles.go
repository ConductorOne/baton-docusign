package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

const permissionProfileAssignedTag = "assigned"

type permissionProfilesBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (p *permissionProfilesBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return p.resourceType
}

func (p *permissionProfilesBuilder) List(ctx context.Context, _ *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var (
		pProfiles []*v2.Resource
		anno      annotations.Annotations
	)

	permissionProfiles, newAnnos, err := p.client.GetPermissionProfiles(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, newAnnotation := range newAnnos {
		anno.Append(newAnnotation)
	}

	for _, permissionProfile := range permissionProfiles {
		if permissionProfile.PermissionProfileId == "" || permissionProfile.PermissionProfileName == "" {
			continue
		}

		permissionProfileResource, err := parseIntoPermissionProfileResource(permissionProfile)
		if err != nil {
			return nil, "", nil, err
		}
		pProfiles = append(pProfiles, permissionProfileResource)
	}

	return pProfiles, "", anno, nil
}

func (p *permissionProfilesBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	newEntitlement := entitlement.NewPermissionEntitlement(
		resource,
		permissionProfileAssignedTag,
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(resource.DisplayName),
		entitlement.WithDescription(resource.Description),
	)
	return []*v2.Entitlement{newEntitlement}, "", nil, nil
}

// Grants would assign permissions to users. This is intentionally left empty as grants are now handled by the userBuilder.
func (p *permissionProfilesBuilder) Grants(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func parseIntoPermissionProfileResource(permissionProfile client.PermissionProfile) (*v2.Resource, error) {
	permissionResource, err := resource.NewRoleResource(
		permissionProfile.PermissionProfileName,
		permissionProfilesResourceType,
		permissionProfile.PermissionProfileId,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create permission profile resource: %w", err)
	}

	return permissionResource, nil
}

// permissionProfilesBuilder creates a new permissionBuilder instance.
func newPermissionProfilesBuilder(client *client.Client) *permissionProfilesBuilder {
	return &permissionProfilesBuilder{
		resourceType: permissionProfilesResourceType,
		client:       client,
	}
}
