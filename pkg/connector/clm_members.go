package connector

import (
	"context"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

// clmMemberBuilder syncs CLM Members — CLM's own principal object. Synced as its own
// resource type rather than reused as the existing `user` resource: identity between
// the two could not be confirmed 1:1.
type clmMemberBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
	includeClm   bool
}

func (b *clmMemberBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmMemberResourceType
}

func (b *clmMemberBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	// include-clm gates whether this does any work at all — not just registration
	// (clm_member is always registered, see connector.go's ResourceSyncers). Without
	// it, skip before ever calling the client: ensureClmReady's CLM base-URL discovery
	// call is itself unconfirmed against a live tenant, and the narrow
	// isOptInFeatureUnavailableError tolerance below only covers a 401/403 from that
	// call, not every other way it could fail (404, 5xx, an unrecognized response
	// schema, a transport error) — those would otherwise fail this whole sync for
	// every account that never opted into CLM.
	if !b.includeClm {
		return nil, &rs.SyncOpResults{}, nil
	}

	var resources []*v2.Resource

	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: clmMemberResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	members, nextPageToken, annos, err := b.client.ListMembers(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		if attr.PageToken.Token == "" && isOptInFeatureUnavailableError(err) {
			ctxzap.Extract(ctx).Info("baton-docusign: CLM is not available for this account or token, skipping clm_member sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, err
	}

	for _, member := range members {
		memberResource, err := parseIntoClmMemberResource(&member)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, memberResource)
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

// Entitlements: clm_member is a pure principal, it holds no entitlements of its own.
func (b *clmMemberBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants: membership/permission grants are emitted from the entitlement-holder's side
// (clm_group, clm_folder) rather than here, per this project's own validated pattern
// for emitting grants from whichever side is cheapest.
func (b *clmMemberBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func newClmMemberBuilder(c *client.Client, includeClm bool) *clmMemberBuilder {
	return &clmMemberBuilder{
		resourceType: clmMemberResourceType,
		client:       c,
		includeClm:   includeClm,
	}
}

// parseIntoClmMemberResource maps a client.ClmMember to a Baton v2.Resource.
func parseIntoClmMemberResource(member *client.ClmMember) (*v2.Resource, error) {
	profile := map[string]any{
		profileFieldEmail:    member.Email,
		"userName":           member.UserName,
		"role":               member.Role,
		"exemptFromUserSync": member.ExemptFromUserSync,
		"portalOnly":         member.PortalOnly,
		"href":               member.Href,
	}

	displayName := member.UserName
	if displayName == "" {
		displayName = member.Email
	}

	userTraits := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithEmail(member.Email, true),
	}

	return rs.NewUserResource(
		displayName,
		clmMemberResourceType,
		clmIDFromHref(member.Href),
		userTraits,
	)
}
