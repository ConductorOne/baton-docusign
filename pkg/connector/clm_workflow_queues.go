package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/session"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

var _ connectorbuilder.StaticEntitlementSyncerV2 = (*clmWorkflowQueueBuilder)(nil)

// entitlementClmWorkflowQueueMember is the single entitlement every CLM workflow queue
// shares — see clmGroupBuilder's entitlementClmGroupMember for the identical pattern.
const entitlementClmWorkflowQueueMember = "member"

// clmSessionKeyQueueMembers builds the session-cache key clm_workflow_queue's List()
// writes to and Grants() reads from — see clmWorkflowQueueBuilder's doc.
func clmSessionKeyQueueMembers(queueID string) string {
	return "clm_workflow_queue_members:" + queueID
}

// clmWorkflowQueueBuilder syncs CLM WorkflowQueues — the API's own term for what the
// CLM admin console reportedly calls "Task Groups" (the surface the customer asked
// for in Pylon #11836). That equivalence is an unconfirmed assumption: no DocuSign
// document states it, and confirming it needs eyes on a real CLM admin console — see
// resource_types.go.
//
// The CLM API has no list-all endpoint for workflow queues and no reverse lookup from a
// queue to its members — the only documented read path is per-member (GET
// .../members/{id}/workflowqueues). So both List() and Grants() are built around a
// single full member scan, not the direct list-all-then-page-per-resource shape every
// other builder in this connector uses:
//
//   - List() (called exactly once — see below) pages through every clm_member via
//     client.ListMembers, and for each one calls client.GetMemberWorkflowQueues. It
//     collects the distinct queues seen (by ID) as the resources to return, and — as a
//     side effect of the same scan — builds a queueID -> []memberID index and writes it
//     to the SDK's session cache (attr.Session), one entry per queue.
//   - Grants(ctx, queueResource, attr) reads that queue's member list straight back out
//     of the session cache instead of re-scanning every member per queue, which would
//     turn one expensive O(members) traversal into O(queues * members) — a real
//     concern on a connector that already has an open rate-limit bug for this same
//     customer (CXP-704).
//
// This only works because the session cache persists for the whole sync (List() runs
// before Grants() for every resource of a type) and is shared across resource types
// within one sync — see cmd/baton-docusign/main.go's
// connectorrunner.WithSessionStoreEnabled().
//
// List() returns every queue it finds in a single page rather than exposing Baton's own
// pagination: the full member scan has to complete before a single queue can be safely
// returned anyway (there's no way to know page 1 of "queues" is complete without having
// scanned every member), so there's nothing to page over — same choice clm_role makes
// for its own small, fully-enumerable set.
//
// Read-only: the API documents work-item assign/unassign, not queue-membership
// grant/revoke, so there's no Grant/Revoke on this builder — matching
// clm_permission_set's precedent for a CLM object with no write endpoint.
//
// Like every other CLM model in this connector, the endpoint shapes here are
// documented-but-unexercised — no live CLM tenant was available to confirm them.
type clmWorkflowQueueBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (b *clmWorkflowQueueBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmWorkflowQueueResourceType
}

func (b *clmWorkflowQueueBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	queuesByID := make(map[string]client.ClmWorkflowQueue)
	membersByQueueID := make(map[string][]string)
	var allAnnos annotations.Annotations

	memberPageToken := ""
	for {
		members, nextMemberPageToken, memberAnnos, err := b.client.ListMembers(ctx, client.PageOptions{PageToken: memberPageToken})
		if err != nil {
			if memberPageToken == "" && isOptInFeatureUnavailableError(err) {
				ctxzap.Extract(ctx).Info("baton-docusign: CLM is not available for this account or token, skipping clm_workflow_queue sync", zap.Error(err))
				return nil, &rs.SyncOpResults{}, nil
			}
			return nil, nil, err
		}
		allAnnos = append(allAnnos, memberAnnos...)

		for _, member := range members {
			memberID := clmIDFromHref(member.Href)
			queues, queueAnnos, err := b.client.GetMemberWorkflowQueues(ctx, memberID)
			if err != nil {
				return nil, nil, fmt.Errorf("getting workflow queues for CLM member %s: %w", memberID, err)
			}
			allAnnos = append(allAnnos, queueAnnos...)

			for _, q := range queues {
				queueID := clmIDFromHref(q.Href)
				queuesByID[queueID] = q
				membersByQueueID[queueID] = append(membersByQueueID[queueID], memberID)
			}
		}

		if nextMemberPageToken == "" {
			break
		}
		memberPageToken = nextMemberPageToken
	}

	for queueID, memberIDs := range membersByQueueID {
		if err := session.SetJSON(ctx, attr.Session, clmSessionKeyQueueMembers(queueID), memberIDs); err != nil {
			return nil, nil, fmt.Errorf("baton-docusign: failed to cache CLM workflow queue %s membership: %w", queueID, err)
		}
	}

	var resources []*v2.Resource
	for _, q := range queuesByID {
		queueResource, err := parseIntoClmWorkflowQueueResource(&q)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, queueResource)
	}

	return resources, &rs.SyncOpResults{Annotations: allAnnos}, nil
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

// Grants reads this queue's membership straight out of the session cache List()
// populated — see this builder's doc for why it doesn't re-scan members here.
func (b *clmWorkflowQueueBuilder) Grants(ctx context.Context, queueResource *v2.Resource, attr rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	memberIDs, found, err := session.GetJSON[[]string](ctx, attr.Session, clmSessionKeyQueueMembers(queueResource.Id.Resource))
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: failed to read cached CLM workflow queue %s membership: %w", queueResource.Id.Resource, err)
	}
	if !found {
		// Expected only if List() didn't actually run first in this sync (shouldn't
		// happen — see this builder's doc) or the session store is disabled. Log loudly
		// and emit zero grants rather than falling back to a per-queue member re-scan,
		// which would be the O(queues * members) cost this design exists to avoid.
		ctxzap.Extract(ctx).Warn("baton-docusign: no cached membership found for CLM workflow queue; emitting zero grants",
			zap.String("queue_id", queueResource.Id.Resource))
		return nil, nil, nil
	}

	grants := make([]*v2.Grant, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		memberResourceId := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: memberID}
		grants = append(grants, grant.NewGrant(queueResource, entitlementClmWorkflowQueueMember, memberResourceId))
	}
	return grants, nil, nil
}

func newClmWorkflowQueueBuilder(c *client.Client) *clmWorkflowQueueBuilder {
	return &clmWorkflowQueueBuilder{
		resourceType: clmWorkflowQueueResourceType,
		client:       c,
	}
}

func parseIntoClmWorkflowQueueResource(q *client.ClmWorkflowQueue) (*v2.Resource, error) {
	return rs.NewGroupResource(
		q.Name,
		clmWorkflowQueueResourceType,
		clmIDFromHref(q.Href),
		nil,
	)
}
