package connector

import (
	"context"
	"errors"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errClmWorkflowQueuesUnavailable is a sentinel discoverClmWorkflowQueueMembership wraps
// its return error with when the very first ListMembers call in the scan fails with
// isOptInFeatureUnavailableError — lets List() distinguish "skip this resource type
// gracefully" from a real failure via errors.Is, without losing the underlying error for
// logging (see List()'s use of it).
var errClmWorkflowQueuesUnavailable = errors.New("baton-docusign: CLM is not available for this account or token")

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
	membership, allAnnos, err := b.discoverClmWorkflowQueueMembership(ctx)
	if err != nil {
		if errors.Is(err, errClmWorkflowQueuesUnavailable) {
			ctxzap.Extract(ctx).Info("baton-docusign: CLM is not available for this account or token, skipping clm_workflow_queue sync", zap.Error(err))
			return nil, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
		}
		return nil, nil, err
	}

	resources := make([]*v2.Resource, 0, len(membership))
	for queueID, entry := range membership {
		if err := session.SetJSON(ctx, attr.Session, clmSessionKeyQueueMembers(queueID), entry.members); err != nil {
			// The session store is opt-in end-to-end: WithSessionStoreEnabled (main.go)
			// only tells the SDK to accept a store connection — whether one actually
			// exists still depends on the parent process wiring a listen port, and it
			// falls back to NoOpSessionStore (every Set call fails) whenever it doesn't.
			// This resource type's Grants() cannot function without it, but that's not
			// true of the rest of the sync — a hard error here would fail every other
			// resource type too. Skip gracefully instead, same as an unavailable CLM
			// subscription.
			ctxzap.Extract(ctx).Warn("baton-docusign: failed to cache CLM workflow queue membership, skipping clm_workflow_queue sync",
				zap.String("queue_id", queueID), zap.Error(err))
			return nil, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
		}

		queueResource, err := parseIntoClmWorkflowQueueResource(&entry.queue)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, queueResource)
	}

	return resources, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
}

// dedupeRateLimitAnnotations keeps every non-rate-limit annotation as-is but collapses
// all RateLimitDescription entries down to the last one — discoverClmWorkflowQueueMembership
// appends one member-page annotation set plus one per-member queue-fetch annotation set
// per iteration, so a large account accumulates thousands of near-identical rate-limit
// snapshots in a single List() response; only the most recent one is meaningful.
func dedupeRateLimitAnnotations(annos annotations.Annotations) annotations.Annotations {
	var out annotations.Annotations
	lastRateLimitIdx := -1
	for i, a := range annos {
		if a.MessageIs(&v2.RateLimitDescription{}) {
			lastRateLimitIdx = i
			continue
		}
		out = append(out, a)
	}
	if lastRateLimitIdx >= 0 {
		out = append(out, annos[lastRateLimitIdx])
	}
	return out
}

// clmWorkflowQueueMembershipEntry pairs a discovered queue with the member IDs found
// to belong to it — the two pieces of data discoverClmWorkflowQueueMembership's member
// scan produces for every queue, kept together under one key instead of two parallel
// maps that would otherwise always share the same key set.
type clmWorkflowQueueMembershipEntry struct {
	queue   client.ClmWorkflowQueue
	members []string
}

// discoverClmWorkflowQueueMembership is the member-scan discovery algorithm this
// builder's whole design is built around (see the type doc above): the CLM API has no
// list-all endpoint for workflow queues and no reverse lookup from a queue to its
// members, so this pages through every clm_member and, for each, calls
// client.GetMemberWorkflowQueues to learn which queues they're in. Pure API-domain
// discovery — no v2.Resource, no session cache — kept separate from List() so this
// algorithm is testable and readable on its own, and List() stays a plain "discover,
// then build resources" shape like every sibling CLM builder's List().
//
// Returns a nil map and an error wrapping errClmWorkflowQueuesUnavailable if the very
// first ListMembers call fails with isOptInFeatureUnavailableError — List() checks for
// that sentinel via errors.Is to decide whether to skip this resource type gracefully.
func (b *clmWorkflowQueueBuilder) discoverClmWorkflowQueueMembership(ctx context.Context) (map[string]*clmWorkflowQueueMembershipEntry, annotations.Annotations, error) {
	membership := make(map[string]*clmWorkflowQueueMembershipEntry)
	var allAnnos annotations.Annotations
	var skippedMembers, skippedQueues int
	var succeededAtLeastOnce bool

	memberPageToken := ""
	for {
		members, nextMemberPageToken, memberAnnos, err := b.client.ListMembers(ctx, client.PageOptions{PageToken: memberPageToken})
		if err != nil {
			if memberPageToken == "" && isOptInFeatureUnavailableError(err) {
				return nil, allAnnos, fmt.Errorf("%w: %w", errClmWorkflowQueuesUnavailable, err)
			}
			return nil, allAnnos, err
		}
		allAnnos = append(allAnnos, memberAnnos...)

		for _, member := range members {
			memberID := clmIDFromHref(member.Href)
			queues, queueAnnos, err := b.client.GetMemberWorkflowQueues(ctx, memberID)
			if err != nil {
				// Only a bare NotFound is tolerated per-member — the isolated "member
				// deleted between ListMembers and this call" case. Unauthenticated/
				// PermissionDenied/FailedPrecondition (the other codes
				// isOptInFeatureUnavailableError covers) signal an account/token-wide
				// problem, same as ListMembers' own first-page check above — tolerating
				// those per-member would silently accept a partial membership set
				// (missing queues, missing members) instead of skipping the whole
				// resource type the way a systemic failure should.
				if status.Code(err) == codes.NotFound {
					skippedMembers++
					if n := skippedMembers; n == 1 || n == 10 || n == 100 || n%1000 == 0 {
						ctxzap.Extract(ctx).Warn("baton-docusign: CLM member not found while scanning workflow queues, skipping",
							zap.String("member_id", memberID), zap.Int("total_occurrences", n), zap.Error(err))
					}
					continue
				}
				if isOptInFeatureUnavailableError(err) && !succeededAtLeastOnce {
					// Same reasoning as ListMembers' own memberPageToken == "" check
					// above: gated on whether any GetMemberWorkflowQueues call has
					// actually succeeded yet, not on len(membership) — a member can
					// legitimately succeed with zero queues, so membership staying empty
					// doesn't mean nothing has been confirmed working. Once at least one
					// call has succeeded, CLM is clearly available, so a later failure
					// here (an expiring token, a scope revoked mid-scan) is a real,
					// isolated problem, not an unavailability signal — failing loud beats
					// discarding every already-discovered queue as if the whole feature
					// were unavailable.
					return nil, allAnnos, fmt.Errorf("%w: %w", errClmWorkflowQueuesUnavailable, err)
				}
				return nil, allAnnos, fmt.Errorf("baton-docusign: getting workflow queues for CLM member %s: %w", memberID, err)
			}
			succeededAtLeastOnce = true
			allAnnos = append(allAnnos, queueAnnos...)

			for _, q := range queues {
				queueID := clmIDFromHref(q.Href)
				if queueID == "" {
					skippedQueues++
					if n := skippedQueues; n == 1 || n == 10 || n == 100 || n%1000 == 0 {
						ctxzap.Extract(ctx).Warn("baton-docusign: CLM workflow queue has an empty Href, skipping",
							zap.String("member_id", memberID), zap.String("queue_name", q.Name), zap.Int("total_occurrences", n))
					}
					continue
				}
				entry, ok := membership[queueID]
				if !ok {
					entry = &clmWorkflowQueueMembershipEntry{queue: q}
					membership[queueID] = entry
				}
				entry.members = append(entry.members, memberID)
			}
		}

		if nextMemberPageToken == "" {
			return membership, allAnnos, nil
		}
		memberPageToken = nextMemberPageToken
	}
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
		// Shouldn't happen — see this builder's doc (List() always runs before Grants()
		// for every resource of a type, and now skips this whole resource type gracefully
		// rather than returning partial results whenever it can't populate the cache).
		// Fail loudly instead of falling back to a per-queue member re-scan (the
		// O(queues * members) cost this design exists to avoid) or silently emitting zero
		// grants, which C1 can't distinguish from this queue's membership having been
		// genuinely emptied out.
		return nil, nil, fmt.Errorf("baton-docusign: no cached membership found for CLM workflow queue %s", queueResource.Id.Resource)
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
