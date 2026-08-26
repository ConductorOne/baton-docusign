package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
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

type permissionProfilesBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (p *permissionProfilesBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return p.resourceType
}

func (p *permissionProfilesBuilder) List(ctx context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var (
		pProfiles []*v2.Resource
		anno      annotations.Annotations
	)

	permissionProfiles, newAnnos, err := p.client.GetPermissionProfiles(ctx)
	if err != nil {
		return nil, nil, err
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
			return nil, nil, err
		}
		pProfiles = append(pProfiles, permissionProfileResource)
	}

	return pProfiles, &rs.SyncOpResults{Annotations: anno}, nil
}

func (p *permissionProfilesBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	newEntitlement := entitlement.NewPermissionEntitlement(
		resource,
		permissionProfileAssignedTag,
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(resource.DisplayName),
		entitlement.WithDescription(resource.Description),
	)
	return []*v2.Entitlement{newEntitlement}, nil, nil
}

// Grants would assign permissions to users. This is intentionally left empty as grants are now handled by the userBuilder.
func (p *permissionProfilesBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
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
// If attempting to revoke the "DocuSign Viewer" profile itself, an error is returned.
// If the user already has "DocuSign Viewer", the operation is idempotent (GrantAlreadyRevoked).
func (p *permissionProfilesBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	userID := grantObj.Principal.Id.Resource
	profileToRevokeID := grantObj.Entitlement.Resource.Id.Resource

	// Get the default "DocuSign Viewer" permission profile ID.
	permissionProfiles, profileAnnos, err := p.client.GetPermissionProfiles(ctx)
	if err != nil {
		return profileAnnos, fmt.Errorf("failed to get permission profiles: %w", err)
	}

	// DocuSign does not enforce unique permission profile names, so more than one profile
	// can share defaultPermissionProfileName. Unlike tryFastPathGrant's name-based grant
	// resolution (users.go), which treats an ambiguous match as not-found rather than risk
	// silently emitting a wrong grant, Revoke restores its pre-existing behavior of taking
	// the first match (same order the API returned them) so that an account with a
	// duplicate-named default profile can still have permission-profile grants revoked at
	// all — just loudly, via the Warn below, instead of silently. See
	// permissionProfilesByName's doc for why this uses a different helper than the
	// ambiguous-is-not-found path.
	matchingProfiles := permissionProfilesByName(permissionProfiles, defaultPermissionProfileName)
	if len(matchingProfiles) == 0 {
		return profileAnnos, fmt.Errorf("default permission profile '%s' not found in account", defaultPermissionProfileName)
	}
	defaultProfileID := matchingProfiles[0].PermissionProfileId
	if len(matchingProfiles) > 1 {
		ctxzap.Extract(ctx).Debug("baton-docusign: default permission profile name is ambiguous, using the first match",
			zap.String("profile_name", defaultPermissionProfileName),
			zap.Int("match_count", len(matchingProfiles)),
			zap.String("chosen_profile_id", defaultProfileID))
	}

	// Check if trying to revoke the default "DocuSign Viewer" profile itself.
	// This is not allowed as it's the minimum permission level.
	if profileToRevokeID == defaultProfileID {
		return profileAnnos, fmt.Errorf(
			"cannot revoke the default '%s' profile (minimum permission level)",
			defaultPermissionProfileName,
		)
	}

	// Get current user details to check their current permission profile.
	userDetail, userAnnos, err := p.client.GetUserDetails(ctx, userID)
	if err != nil {
		return userAnnos, fmt.Errorf("failed to get user details: %w", err)
	}

	// If the user already has the default "DocuSign Viewer" profile,
	// the revoke operation is already complete (idempotent).
	if userDetail.PermissionProfileName == defaultPermissionProfileName {
		return annotations.New(&v2.GrantAlreadyRevoked{}), nil
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
	permissionResource, err := rs.NewRoleResource(
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
