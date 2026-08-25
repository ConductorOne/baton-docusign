package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ connectorbuilder.StaticEntitlementSyncerV2 = (*clmGroupBuilder)(nil)

// Entitlement value representing CLM group membership. Distinct constant from
// entitlementGroupMember (groups.go) even though the slug string is the same value —
// clm_group is a different upstream object from eSignature's group, see resource_types.go.
const entitlementClmGroupMember = "member"

// clmGroupBuilder implements resource listing, entitlements, and grants for CLM
// Groups. Unlike eSignature's groups.go, Grant/Revoke are NOT implemented on the
// Groups object itself — the CLM Groups object has zero write methods. Membership is
// granted/revoked from the MEMBER side instead:
// PATCH .../members/{id} (additive, Grant) and PUT .../members/{id} (full-replace,
// Revoke) — both are read-modify-write against the member's current group list.
// Uses StaticEntitlementSyncerV2 since every CLM group shares the same single "member"
// entitlement (see resource_types.go's SkipEntitlements annotation on this type).
type clmGroupBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (g *clmGroupBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmGroupResourceType
}

func (g *clmGroupBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var resources []*v2.Resource

	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: clmGroupResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	groups, nextPageToken, annos, err := g.client.ListGroups(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		if attr.PageToken.Token == "" && isOptInFeatureUnavailableError(err) {
			ctxzap.Extract(ctx).Info("baton-docusign: CLM is not available for this account or token, skipping clm_group sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, err
	}

	for _, grp := range groups {
		groupResource, err := parseIntoClmGroupResource(&grp)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, groupResource)
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

// Entitlements returns nil — the SDK does not call this when StaticEntitlementSyncerV2
// is implemented (see StaticEntitlements below).
func (g *clmGroupBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// StaticEntitlements declares the single "member" entitlement every CLM group shares,
// stamped by the SDK onto every synced clm_group resource.
func (g *clmGroupBuilder) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	ent := v2.Entitlement_builder{
		Slug:        entitlementClmGroupMember,
		DisplayName: "Member",
		Description: "Member of this CLM group",
		Purpose:     v2.Entitlement_PURPOSE_VALUE_ASSIGNMENT,
		GrantableTo: []*v2.ResourceType{clmMemberResourceType},
	}.Build()
	return []*v2.Entitlement{ent}, nil, nil
}

func (g *clmGroupBuilder) Grants(ctx context.Context, groupResource *v2.Resource, attr rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: clmMemberResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	members, nextPageToken, annos, err := g.client.GetGroupMembers(ctx, groupResource.Id.Resource, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: getting members for CLM group %s: %w", groupResource.Id.Resource, err)
	}

	grants := make([]*v2.Grant, 0, len(members))
	for _, member := range members {
		memberResourceId := &v2.ResourceId{
			ResourceType: clmMemberResourceType.Id,
			Resource:     clmIDFromHref(member.Href),
		}
		grants = append(grants, grant.NewGrant(
			groupResource,
			entitlementClmGroupMember,
			memberResourceId,
		))
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}

	return grants, &rs.SyncOpResults{
		NextPageToken: outToken,
		Annotations:   annos,
	}, nil
}

// Grant adds a CLM member to a CLM group. Read-modify-write: fetch the member's
// current groups, skip if already a member (idempotent), else PATCH with the target
// group appended (additive per the confirmed Members.Patch semantics).
func (g *clmGroupBuilder) Grant(ctx context.Context, principal *v2.Resource, ent *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	if principal.Id.ResourceType != clmMemberResourceType.Id {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-docusign: invalid principal type: expected %s, got %s", clmMemberResourceType.Id, principal.Id.ResourceType)
	}

	memberID := principal.Id.Resource
	groupID := ent.Resource.Id.Resource

	// clmIDFromHref reduces an empty Href to "" too, so an empty groupID or memberID
	// would falsely match a degenerate currentGroups entry below (empty groupID hits the
	// "already a member" check before clmPreferredHref's own empty-id guard ever runs) —
	// same class of bug as clm_folders.go's Grant/Revoke, guarded the same way.
	if memberID == "" || groupID == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-docusign: granting CLM group membership: member or group missing native ID")
	}

	// Don't require the group's Href off ent.Resource: the pebble storage engine hydrates
	// an entitlement's Resource as an identity-only stub (no profile, no annotations), so
	// it isn't always available. The groupHref this Grant actually writes below is
	// resolved via clmPreferredHref, preferring (in order): ent.Resource's own profile
	// Href if present, then a real sample Href from currentGroups, else client.GroupHref.
	currentGroups, annos, err := g.client.GetMemberGroups(ctx, memberID)
	if err != nil {
		return nil, annos, fmt.Errorf("baton-docusign: getting current groups for CLM member %s: %w", memberID, err)
	}

	for _, current := range currentGroups {
		if clmIDFromHref(current.Href) == groupID {
			// Already a member — idempotent Grant.
			return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
		}
	}

	// Prefer a real, server-issued Href already on hand over one derived from the
	// discovered CLM base URL — see clmPreferredHref's doc. The target group's own
	// profile href (parseIntoClmGroupResource still populates it for display) comes
	// first: it's this exact group's own recorded Href, not a sibling's (one of this
	// member's OTHER current groups) to derive from — only ever a sample, never
	// required, so an identity-only ent.Resource (the pebble-hydrated case this fix
	// exists for) still falls through to the other-groups/fallback path unchanged.
	sampleHrefs := clmSampleHrefsFrom(ent.Resource, currentGroups, func(g client.ClmGroup) string { return g.Href })
	groupHref, err := clmPreferredHref(ctx, groupID, sampleHrefs, func() (string, error) {
		return g.client.GroupHref(ctx, groupID)
	})
	if err != nil {
		return nil, annos, fmt.Errorf("baton-docusign: resolving href for CLM group %s: %w", groupID, err)
	}

	newGroups := make([]client.ClmGroup, 0, len(currentGroups)+1)
	newGroups = append(newGroups, currentGroups...)
	newGroups = append(newGroups, client.ClmGroup{Href: groupHref})
	patchAnnos, err := g.client.PatchMemberGroups(ctx, memberID, newGroups)
	if err != nil {
		return nil, patchAnnos, fmt.Errorf("baton-docusign: granting CLM group membership: %w", err)
	}

	return nil, patchAnnos, nil
}

// Revoke removes a CLM member from a CLM group. Read-modify-write: fetch the member's
// current groups, skip if already not a member (idempotent), else PUT with the target
// group omitted (full-replace per the confirmed Members.Put semantics — "removed from
// unspecified groups").
func (g *clmGroupBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	memberID := grantObj.Principal.Id.Resource
	groupID := grantObj.Entitlement.Resource.Id.Resource

	// Same guard as Grant, and for the same reason: an empty groupID would falsely match
	// a currentGroups entry with an empty Href (clmIDFromHref("") == ""), excluding it
	// from remainingGroups — and PutMemberGroups is a full-replace, so that unrelated
	// membership would actually be removed from the real account.
	if memberID == "" || groupID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "baton-docusign: revoking CLM group membership: member or group missing native ID")
	}

	currentGroups, annos, err := g.client.GetMemberGroups(ctx, memberID)
	if err != nil {
		return annos, fmt.Errorf("baton-docusign: getting current groups for CLM member %s: %w", memberID, err)
	}

	remainingGroups := make([]client.ClmGroup, 0, len(currentGroups))
	found := false
	for _, current := range currentGroups {
		if clmIDFromHref(current.Href) == groupID {
			found = true
			continue
		}
		remainingGroups = append(remainingGroups, current)
	}

	if !found {
		return annotations.New(&v2.GrantAlreadyRevoked{}), nil
	}

	putAnnos, err := g.client.PutMemberGroups(ctx, memberID, remainingGroups)
	if err != nil {
		return putAnnos, fmt.Errorf("baton-docusign: revoking CLM group membership: %w", err)
	}

	return putAnnos, nil
}

func newClmGroupBuilder(c *client.Client) *clmGroupBuilder {
	return &clmGroupBuilder{
		resourceType: clmGroupResourceType,
		client:       c,
	}
}

// parseIntoClmGroupResource maps a client.ClmGroup to a Baton v2.Resource. The Href is
// kept in the profile both for display and as the preferred sample href for Grant;
// Grant falls back to client.GroupHref when it's absent, since neither a profile nor an
// annotation is guaranteed to survive to where it's needed.
func parseIntoClmGroupResource(group *client.ClmGroup) (*v2.Resource, error) {
	profile := map[string]any{
		"name":           group.Name,
		"groupType":      group.GroupType,
		profileFieldHref: group.Href,
	}

	return rs.NewGroupResource(
		group.Name,
		clmGroupResourceType,
		clmIDFromHref(group.Href),
		nil,
		rs.WithResourceProfile(profile),
	)
}
