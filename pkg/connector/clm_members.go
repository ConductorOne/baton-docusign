package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/protobuf/proto"
)

// clmMemberBuilder syncs CLM Members — CLM's own principal object. Synced as its own
// resource type rather than reused as the existing `user` resource: identity between
// the two could not be confirmed 1:1. Also emits clm_workflow_queue membership grants
// from Grants() — see that method's doc comment for why the principal side owns them.
type clmMemberBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
	// includeWorkflowQueues reports whether clm_workflow_queue is included in the
	// customer's sync filter. When false, ResourceType() attaches
	// SkipEntitlementsAndGrants so the SDK never calls Grants() — which would
	// otherwise invoke GetMemberWorkflowQueues even though the customer didn't
	// opt into workflow queues (mirrors userBuilder + skipPermissionProfileResourceType).
	includeWorkflowQueues bool
}

// ResourceType returns the Baton resource type handled by this builder,
// annotated to tell the SDK's sync engine whether it can skip calling
// Grants() for clm_member resources. clmMemberResourceType is a package-level
// var shared with other code, so it's cloned before its annotations are mutated.
func (b *clmMemberBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	rt := proto.Clone(clmMemberResourceType).(*v2.ResourceType)
	annos := annotations.Annotations(rt.Annotations)
	if b.includeWorkflowQueues {
		annos.Update(&v2.SkipEntitlements{})
	} else {
		annos.Update(&v2.SkipEntitlementsAndGrants{})
	}
	rt.Annotations = annos
	return rt
}

func (b *clmMemberBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
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

// Grants emits this member's clm_workflow_queue membership — the one exception to this
// project's usual "emit from the entitlement-holder's side" pattern (clm_group,
// clm_folder do that instead). CLM's API only exposes workflow-queue membership per
// member (GetMemberWorkflowQueues), not per-queue, so the principal side is the only
// side that can produce this. clmWorkflowQueueBuilder.List() (this member's child-resource
// sync) makes the same GetMemberWorkflowQueues call earlier in the sync to discover queue
// resources; this Grants() call repeats it once per member — an accepted 2x tradeoff of
// the ChildResourceType design (no session store between List and Grants phases). That
// also means membership can drift between the resources and grants phases on very long
// syncs (a grant could reference a queue resource not yet stored); we accept that skew
// rather than reintroduce session-store coupling per the ChildResourceType redesign.
func (b *clmMemberBuilder) Grants(ctx context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	memberID := resource.Id.Resource

	queues, annos, err := b.client.GetMemberWorkflowQueues(ctx, memberID)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: getting CLM workflow queues for member %s: %w", memberID, err)
	}

	grants := make([]*v2.Grant, 0, len(queues))
	for _, q := range queues {
		queueID := clmIDFromHref(q.Href)
		if queueID == "" {
			logSkippedClmWorkflowQueueWithEmptyHref(ctx, memberID, q.Href)
			continue
		}
		queueResourceID := &v2.ResourceId{ResourceType: clmWorkflowQueueResourceType.Id, Resource: queueID}
		grants = append(grants, grant.NewGrant(&v2.Resource{Id: queueResourceID}, entitlementClmWorkflowQueueMember, resource.Id))
	}

	return grants, &rs.SyncOpResults{Annotations: annos}, nil
}

func newClmMemberBuilder(c *client.Client, includeWorkflowQueues bool) *clmMemberBuilder {
	return &clmMemberBuilder{
		resourceType:          clmMemberResourceType,
		client:                c,
		includeWorkflowQueues: includeWorkflowQueues,
	}
}

// parseIntoClmMemberResource maps a client.ClmMember to a Baton v2.Resource. The Href is
// kept in the profile both for display and as the preferred sample href for Grant;
// Grant falls back to client.MemberHref when it's absent, since neither a profile nor
// an annotation is guaranteed to survive to where it's needed.
//
// Stamps the ChildResourceType annotation on every instance, mirroring the one declared
// on clmMemberResourceType itself — the SDK's child-resource scheduling
// (childResourceTypeIDs, pkg/sync/syncer.go) reads it off each resource instance, not
// the type declaration, so this is what actually triggers a clm_workflow_queue.List()
// call per member.
func parseIntoClmMemberResource(member *client.ClmMember) (*v2.Resource, error) {
	profile := map[string]any{
		profileFieldEmail:    member.Email,
		"userName":           member.UserName,
		"role":               member.Role,
		"exemptFromUserSync": member.ExemptFromUserSync,
		"portalOnly":         member.PortalOnly,
		profileFieldHref:     member.Href,
	}

	displayName := member.UserName
	if displayName == "" {
		displayName = member.Email
	}

	userTraits := []rs.UserTraitOption{
		rs.WithEmail(member.Email, true),
	}

	return rs.NewUserResource(
		displayName,
		clmMemberResourceType,
		clmIDFromHref(member.Href),
		userTraits,
		rs.WithResourceProfile(profile),
		rs.WithAnnotation(&v2.ChildResourceType{ResourceTypeId: clmWorkflowQueueResourceType.Id}),
	)
}
