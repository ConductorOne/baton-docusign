package connector

import (
	"context"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

// clmPermissionSetAssignedTag mirrors permissionProfileAssignedTag's pattern for the
// eSignature permission_profile resource type.
const clmPermissionSetAssignedTag = "assigned"

// clmPermissionSetBuilder syncs CLM PermissionSets. Read-only for grants: no
// assignment endpoint exists anywhere in the CLM API — this mirrors
// permissionProfilesBuilder's read-mostly shape, but unlike that builder there
// is no other object (like userBuilder) that can emit the assignment grants either, so
// Grants() here is a hard no-op rather than being handled elsewhere.
type clmPermissionSetBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (b *clmPermissionSetBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmPermissionSetResourceType
}

func (b *clmPermissionSetBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var resources []*v2.Resource

	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: clmPermissionSetResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	permissionSets, nextPageToken, annos, err := b.client.ListPermissionSets(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		if attr.PageToken.Token == "" && isOptInFeatureUnavailableError(err) {
			ctxzap.Extract(ctx).Info("baton-docusign: CLM is not available for this account or token, skipping clm_permission_set sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, err
	}

	for _, ps := range permissionSets {
		psResource, err := parseIntoClmPermissionSetResource(&ps)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, psResource)
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}

	return resources, &rs.SyncOpResults{
		Annotations:   annos,
		NextPageToken: outToken,
	}, nil
}

// Entitlements declares the permission set as visibility-only: no WithGrantableTo,
// since (unlike permission_profiles.go's equivalent, which is granted/revoked from the
// user side) there is no Grant/Revoke path anywhere in this connector for CLM
// permission sets — no assignment endpoint exists in the CLM API at all. Declaring a
// GrantableTo here would show this as assignable in the C1 UI when it never actually
// can be, via any principal.
func (b *clmPermissionSetBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	newEntitlement := entitlement.NewPermissionEntitlement(
		resource,
		clmPermissionSetAssignedTag,
		entitlement.WithDisplayName(resource.DisplayName),
		entitlement.WithDescription(resource.Description),
	)
	return []*v2.Entitlement{newEntitlement}, nil, nil
}

// Grants: unsupported by the API, not "not implemented yet" — no endpoint anywhere in
// the CLM API links a member/group to a permission set as an assignment. PermissionSets
// sync for visibility only.
func (b *clmPermissionSetBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func newClmPermissionSetBuilder(c *client.Client) *clmPermissionSetBuilder {
	return &clmPermissionSetBuilder{
		resourceType: clmPermissionSetResourceType,
		client:       c,
	}
}

func parseIntoClmPermissionSetResource(ps *client.ClmPermissionSet) (*v2.Resource, error) {
	// structpb.NewStruct (used by WithResourceProfile) only accepts []interface{} for list
	// values, not []string, so ps.Permissions needs converting before it goes in the
	// profile map.
	permissions := make([]any, len(ps.Permissions))
	for i, p := range ps.Permissions {
		permissions[i] = p
	}

	profile := map[string]any{
		"permissions": permissions,
	}

	return rs.NewRoleResource(
		ps.Name,
		clmPermissionSetResourceType,
		clmIDFromHref(ps.Href),
		nil,
		rs.WithResourceProfile(profile),
	)
}
