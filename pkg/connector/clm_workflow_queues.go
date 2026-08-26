package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

var _ connectorbuilder.StaticEntitlementSyncerV2 = (*clmWorkflowQueueBuilder)(nil)

// entitlementClmWorkflowQueueMember is the single entitlement every CLM workflow queue
// shares — see clmGroupBuilder's entitlementClmGroupMember for the identical pattern.
const entitlementClmWorkflowQueueMember = "member"

// clmWorkflowQueueBuilder syncs CLM WorkflowQueues (UI "Task Groups" — unconfirmed) as
// clmMemberResourceType's ChildResourceType (see that type's doc comment). CLM exposes
// workflow-queue membership only per member (GetMemberWorkflowQueues) — there is no
// list-all-queues endpoint — so List() here is driven per-member by the SDK rather than
// paginating independently. A queue with several members is discovered once per member
// whose scan reaches it; the SDK upserts resources by (ResourceType, Resource) regardless
// of which parent scan produced them, so repeat discovery is an idempotent identity
// re-upsert — parent is not modeled on the resource because a shared queue has no
// single canonical member parent (see parseIntoClmWorkflowQueueResource).
// Membership grants are emitted from clmMemberBuilder.Grants() instead of here — the
// query is per-member either way, and emitting from the principal side avoids needing
// durable state between the resources and grants sync phases (contrast the previous
// session-store-accumulator design this replaced). That does mean GetMemberWorkflowQueues
// runs twice per member per sync (once here in List(), once in clmMemberBuilder.Grants())
// — an accepted tradeoff of this ChildResourceType design.
type clmWorkflowQueueBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (b *clmWorkflowQueueBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmWorkflowQueueResourceType
}

// List returns every workflow queue parentResourceID (a clm_member) belongs to. Called
// once per synced clm_member by the SDK's child-resource scheduling (driven by the
// ChildResourceType annotation clmMemberBuilder stamps on every member resource) — not
// paginated on its own, since GetMemberWorkflowQueues already pages CLM's response to
// completion internally.
func (b *clmWorkflowQueueBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if parentResourceID == nil {
		// The SDK always issues one unparented top-level List() per registered resource
		// type (syncer.go SyncResources) in addition to parented child-resource calls.
		// clm_workflow_queue is child-only — queues are discovered per clm_member via
		// ChildResourceType scheduling — so the unparented call is a no-op.
		return nil, nil, nil
	}

	queues, annos, err := b.client.GetMemberWorkflowQueues(ctx, parentResourceID.Resource)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: getting CLM workflow queues for member %s: %w", parentResourceID.Resource, err)
	}

	resources := make([]*v2.Resource, 0, len(queues))
	for _, q := range queues {
		if clmIDFromHref(q.Href) == "" {
			logSkippedClmWorkflowQueueWithEmptyHref(ctx, parentResourceID.Resource, q.Href)
			continue
		}
		queueResource, err := parseIntoClmWorkflowQueueResource(&q)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, queueResource)
	}

	return resources, &rs.SyncOpResults{Annotations: annos}, nil
}

// Entitlements returns nil — the SDK does not call this when StaticEntitlementSyncerV2
// is implemented (see StaticEntitlements below).
func (b *clmWorkflowQueueBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// StaticEntitlements declares the single "member" entitlement every CLM workflow queue
// shares, stamped by the SDK onto every synced clm_workflow_queue resource.
func (b *clmWorkflowQueueBuilder) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	ent := v2.Entitlement_builder{
		Slug:        entitlementClmWorkflowQueueMember,
		DisplayName: "Member",
		Description: "Member of this CLM workflow queue",
		Purpose:     v2.Entitlement_PURPOSE_VALUE_ASSIGNMENT,
		GrantableTo: []*v2.ResourceType{clmMemberResourceType},
	}.Build()
	return []*v2.Entitlement{ent}, nil, nil
}

// Grants: membership grants are emitted from clmMemberBuilder.Grants() instead — see
// this builder's type doc comment for why the principal side owns them.
func (b *clmWorkflowQueueBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func newClmWorkflowQueueBuilder(c *client.Client) *clmWorkflowQueueBuilder {
	return &clmWorkflowQueueBuilder{
		resourceType: clmWorkflowQueueResourceType,
		client:       c,
	}
}

// parseIntoClmWorkflowQueueResource maps a client.ClmWorkflowQueue to a Baton v2.Resource.
// No ParentResourceId is stamped: a queue can belong to several members, so the
// discovering member's ID would be last-writer-wins across syncs rather than a stable
// canonical parent.
func parseIntoClmWorkflowQueueResource(q *client.ClmWorkflowQueue) (*v2.Resource, error) {
	return rs.NewGroupResource(
		q.Name,
		clmWorkflowQueueResourceType,
		clmIDFromHref(q.Href),
		nil,
	)
}
