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
		return nil, nil, fmt.Errorf("getting members for CLM group %s: %w", groupResource.Id.Resource, err)
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
		return nil, nil, fmt.Errorf("baton-docusign: invalid principal type: expected %s, got %s", clmMemberResourceType.Id, principal.Id.ResourceType)
	}

	memberID := principal.Id.Resource
	groupID := ent.Resource.Id.Resource
	groupHref, err := clmGroupHrefFromResource(ent.Resource)
	if err != nil {
		return nil, nil, err
	}

	currentGroups, annos, err := g.client.GetMemberGroups(ctx, memberID)
	if err != nil {
		return nil, annos, fmt.Errorf("getting current groups for CLM member %s: %w", memberID, err)
	}

	for _, current := range currentGroups {
		if clmIDFromHref(current.Href) == groupID {
			// Already a member — idempotent Grant.
			return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
		}
	}

	newGroups := make([]client.ClmGroup, 0, len(currentGroups)+1)
	newGroups = append(newGroups, currentGroups...)
	newGroups = append(newGroups, client.ClmGroup{Href: groupHref})
	patchAnnos, err := g.client.PatchMemberGroups(ctx, memberID, newGroups)
	if err != nil {
		return nil, patchAnnos, fmt.Errorf("granting CLM group membership: %w", err)
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

	currentGroups, annos, err := g.client.GetMemberGroups(ctx, memberID)
	if err != nil {
		return annos, fmt.Errorf("getting current groups for CLM member %s: %w", memberID, err)
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
		return putAnnos, fmt.Errorf("revoking CLM group membership: %w", err)
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
// carried in the profile (not just used to derive the ResourceId) so Grant() can
// reconstruct a reference to this group without needing to guess a URL — see
// clmGroupHrefFromResource.
func parseIntoClmGroupResource(group *client.ClmGroup) (*v2.Resource, error) {
	profile := map[string]any{
		"name":      group.Name,
		"groupType": group.GroupType,
		"href":      group.Href,
	}

	return rs.NewGroupResource(
		group.Name,
		clmGroupResourceType,
		clmIDFromHref(group.Href),
		[]rs.GroupTraitOption{rs.WithGroupProfile(profile)},
	)
}

// clmGroupHrefFromResource reads back the Href stashed in a CLM group resource's
// profile (see parseIntoClmGroupResource) — needed to reference the group in a
// Members.Patch grant body.
func clmGroupHrefFromResource(groupResource *v2.Resource) (string, error) {
	trait, err := rs.GetGroupTrait(groupResource)
	if err != nil {
		return "", fmt.Errorf("baton-docusign: failed to read CLM group trait: %w", err)
	}
	href := trait.GetProfile().GetFields()["href"].GetStringValue()
	if href == "" {
		return "", fmt.Errorf("baton-docusign: CLM group resource %s is missing its href profile field", groupResource.Id.Resource)
	}
	return href, nil
}
