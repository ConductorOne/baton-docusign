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

const (
	permissionProfileAssignedTag = "assigned"
	// defaultPermissionProfileName is the built-in DocuSign profile with minimal permissions.
	// This profile exists in all DocuSign accounts and cannot be deleted or edited.
	// According to DocuSign documentation, you cannot edit the DS Admin, DS Sender, and DS Viewer
	// permission profiles (where "DS" is the abbreviation for "DocuSign"). The API returns this
	// profile with the name "DocuSign Viewer" (same as "DS Viewer", just the full name).
	// If the profile name differs from the expected value (e.g., due to API version differences),
	// the Revoke operation will fail with a descriptive error message.
	// It's used as the fallback when revoking permission profiles (users must always have a profile).
	//
	// Documentation Update: https://support.docusign.com/s/document-item?language=en_US&rsc_301&bundleId=pik1583277475390&topicId=qzx1583277361124.html&_LANG=enus
	// Documentation Delete: https://support.docusign.com/s/document-item?language=en_US&rsc_301&bundleId=pik1583277475390&topicId=chp1583277361564.html&_LANG=enus
	defaultPermissionProfileName = "DocuSign Viewer"
)

type permissionProfilesClientInterface interface {
	GetPermissionProfiles(ctx context.Context) ([]client.PermissionProfile, annotations.Annotations, error)
	GetUserDetails(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error)
	UpdateUserProfile(ctx context.Context, userID string, request client.UpdateUserProfileRequest) (annotations.Annotations, error)
}

type permissionProfilesBuilder struct {
	resourceType *v2.ResourceType
	client       permissionProfilesClientInterface
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

// Grant assigns a permission profile to a user by updating the user's profile.
func (p *permissionProfilesBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	if principal.Id.ResourceType != userResourceType.Id {
		return nil, nil, fmt.Errorf("invalid principal type: expected %s, got %s", userResourceType.Id, principal.Id.ResourceType)
	}

	permissionProfileID := entitlement.Resource.Id.Resource
	userID := principal.Id.Resource

	request := client.UpdateUserProfileRequest{}
	request.UserDetails.PermissionProfileId = permissionProfileID

	annos, err := p.client.UpdateUserProfile(ctx, userID, request)
	if err != nil {
		return nil, annos, fmt.Errorf("failed to grant permission profile: %w", err)
	}

	return nil, annos, nil
}

// Revoke "removes" a permission profile from a user by assigning the default "DocuSign Viewer" profile.
// Note: DocuSign requires users to always have a permission profile assigned.
// Revoking a permission profile assigns the user to "DocuSign Viewer" (the most basic, read-only profile).
func (p *permissionProfilesBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	userID := grantObj.Principal.Id.Resource

	// Get the default "DocuSign Viewer" permission profile ID.
	permissionProfiles, _, err := p.client.GetPermissionProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get permission profiles: %w", err)
	}

	var defaultProfileID string
	for _, profile := range permissionProfiles {
		if profile.PermissionProfileName == defaultPermissionProfileName {
			defaultProfileID = profile.PermissionProfileId
			break
		}
	}

	if defaultProfileID == "" {
		return nil, fmt.Errorf("default permission profile '%s' not found in account", defaultPermissionProfileName)
	}

	// Assign default "DocuSign Viewer" profile.
	request := client.UpdateUserProfileRequest{}
	request.UserDetails.PermissionProfileId = defaultProfileID

	annos, err := p.client.UpdateUserProfile(ctx, userID, request)
	if err != nil {
		return annos, fmt.Errorf("failed to assign default permission profile: %w", err)
	}

	return annos, nil
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
