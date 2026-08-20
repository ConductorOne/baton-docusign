package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/session"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// clmWorkflowQueueUnavailableThreshold is how many CONSECUTIVE tolerated per-member
// failures (with nothing yet successfully scanned) List() requires, across however many
// chunked calls it takes to see them, before concluding the whole account can't use this
// endpoint — see the escalation branch's doc for why a single failure isn't enough. An
// arbitrary but deliberately small judgment call: no live CLM tenant to derive it from
// empirically.
const clmWorkflowQueueUnavailableThreshold = 3

// clmSessionKeyWorkflowQueueDiscoveryState is where List() persists its accumulating
// member-scan state between its own successive SDK-driven calls — see List()'s doc for
// why the scan is chunked this way instead of running to completion inside one call.
const clmSessionKeyWorkflowQueueDiscoveryState = "clm_workflow_queue_discovery_state"

// clmWorkflowQueueDiscoveryState is List()'s accumulator: the registry of distinct
// queues the scan has found so far (metadata only — member lists are persisted
// separately and incrementally, see List()'s doc), plus the escalation-threshold
// counters that must span every chunk of the scan, not reset per chunk (three tolerated
// failures on members split across two separate List() calls must still escalate,
// exactly as three in one call would). Exported fields: this round-trips through
// session.SetJSON/GetJSON (encoding/json can't see unexported fields).
type clmWorkflowQueueDiscoveryState struct {
	Queues                         map[string]client.ClmWorkflowQueue `json:"queues"`
	SucceededAtLeastOnce           bool                               `json:"succeeded_at_least_once"`
	ConsecutiveUnavailableFailures int                                `json:"consecutive_unavailable_failures"`
	SkippedMembers                 int                                `json:"skipped_members"`
	SkippedMembersNoID             int                                `json:"skipped_members_no_id"`
	SkippedQueues                  int                                `json:"skipped_queues"`
	// NextExpectedInputToken is the ListMembers page token the next call should arrive
	// with. Any other incoming token (a resumed sync rolling back one or more chunks)
	// is a replay: List() resumes from this frontier instead of re-running an
	// already-applied chunk and double-counting the escalation counters above.
	NextExpectedInputToken string `json:"next_expected_input_token"`
	// ScanComplete distinguishes "the scan finished" from "nothing processed yet" — both
	// otherwise look identical (NextExpectedInputToken == ""). Without it, a replay of a
	// scan that completed in a single page wouldn't be detected as a replay at all.
	ScanComplete bool `json:"scan_complete"`
}

var _ connectorbuilder.StaticEntitlementSyncerV2 = (*clmWorkflowQueueBuilder)(nil)

// entitlementClmWorkflowQueueMember is the single entitlement every CLM workflow queue
// shares — see clmGroupBuilder's entitlementClmGroupMember for the identical pattern.
const entitlementClmWorkflowQueueMember = "member"

// clmSessionKeyQueueMembers builds the session-cache key clm_workflow_queue's List()
// writes to and Grants() reads from — see clmWorkflowQueueBuilder's doc.
func clmSessionKeyQueueMembers(queueID string) string {
	return "clm_workflow_queue_members:" + queueID
}

// clmWorkflowQueueBuilder syncs CLM WorkflowQueues — the API's own term for what the CLM
// admin console reportedly calls "Task Groups". That equivalence is an unconfirmed
// assumption: no DocuSign document states it, and confirming it needs eyes on a real CLM
// admin console — see resource_types.go.
//
// The CLM API has no list-all endpoint for workflow queues and no reverse lookup from a
// queue to its members — the only documented read path is per-member (GET
// .../members/{id}/workflowqueues). So both List() and Grants() are built around a
// member scan, not the direct list-all-then-page-per-resource shape every other builder
// in this connector uses:
//
//   - List() chunks the scan one ListMembers page per SDK-driven call — the usual
//     one-page-per-call shape every sibling builder in this connector uses, unlike an
//     earlier version of this file that ran the entire member scan (every ListMembers
//     page, plus a GetMemberWorkflowQueues call per member) inside a single call. Each
//     chunk merges its own contribution into every touched queue's own session key
//     (clmSessionKeyQueueMembers) rather than accumulating one all-queues blob in
//     memory: a single growing value would both rewrite on every chunk (O(members^2)
//     session-store traffic over a full scan) and risk crossing the session store's
//     per-value size ceiling on a large account. Bounds this per queue, not per member —
//     a queue most members belong to still rewrites its own growing list each chunk. A
//     separate, small
//     clmWorkflowQueueDiscoveryState blob — just the distinct queues seen so far plus
//     the escalation-threshold counters — persists across chunks instead, since a plain
//     Go value doesn't survive across separate List() invocations. Only on the LAST
//     member page — the queue set can't be confirmed complete before then (there's no
//     list-all endpoint to check it against) — does it emit the discovered queues as
//     resources; every earlier chunk returns zero resources plus a NextPageToken.
//   - Grants(ctx, queueResource, attr) reads that queue's member list straight back out
//     of the session cache instead of re-scanning every member per queue, which would
//     turn one expensive O(members) traversal into O(queues * members) API calls against
//     an already rate-limited endpoint.
//
// This only works because the session cache persists for the whole sync (across all of
// List()'s own chunked calls, and through to when Grants() runs for every resource of
// this type afterward) and is shared across resource types within one sync — see
// cmd/baton-docusign/main.go's connectorrunner.WithSessionStoreEnabled().
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

// List drains one ListMembers page per call — see clmWorkflowQueueBuilder's doc for why
// the member scan is chunked this way, and clmWorkflowQueueDiscoveryState for what gets
// persisted across chunks. Escalation/error-tolerance semantics for
// GetMemberWorkflowQueues failures are unchanged from a single-call scan: a tolerated
// code (PermissionDenied/Unauthenticated/NotFound/FailedPrecondition) before anything has
// succeeded counts toward clmWorkflowQueueUnavailableThreshold regardless of which chunk
// it lands in, and fails the sync loudly once reached; a NotFound after something has
// already succeeded is an isolated skip (an ordinary mid-scan deletion race, unrelated to
// whether this account can use CLM); any other tolerated code after success fails loud.
func (b *clmWorkflowQueueBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: clmWorkflowQueueResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	state, found, err := session.GetJSON[clmWorkflowQueueDiscoveryState](ctx, attr.Session, clmSessionKeyWorkflowQueueDiscoveryState)
	if err != nil {
		if pageToken != "" {
			// A non-empty incoming page token can only exist because an earlier chunk's
			// session-store writes already succeeded — state.SucceededAtLeastOnce isn't
			// available here (that's exactly what this failed read was trying to fetch),
			// but the token itself proves real queue data is already durably persisted.
			// Losing it now would read as every previously discovered queue having been
			// deleted, so fail loud instead of accepting a lossy empty result.
			return nil, nil, fmt.Errorf("baton-docusign: failed to read CLM workflow queue discovery state: %w", err)
		}
		// The session store is opt-in end-to-end: WithSessionStoreEnabled (main.go) only
		// tells the SDK to accept a store connection — whether one actually exists still
		// depends on the parent process wiring a listen port, and it falls back to
		// NoOpSessionStore (every Get call fails too) whenever it doesn't. This resource
		// type's Grants() cannot function without the cache, but that's not true of the
		// rest of the sync — a hard error here would fail every other resource type too.
		// Skip gracefully instead, same as an unavailable CLM subscription.
		ctxzap.Extract(ctx).Debug("baton-docusign: failed to read CLM workflow queue discovery state, skipping clm_workflow_queue sync", zap.Error(err))
		return nil, &rs.SyncOpResults{}, nil
	}
	if !found && pageToken != "" {
		// A non-empty incoming page token means an earlier chunk already ran and wrote
		// real progress, but the read above returned no error and no state — the entry
		// itself is simply gone from the store (e.g. an in-memory store that restarted
		// between chunks, or an expired entry). Silently restarting the scan from a
		// zero-value state here would discover only the tail of the queue set from here
		// on, and the final chunk would emit that partial set as the authoritative
		// result — every queue an earlier chunk already found would read as deleted, the
		// same false-deletion outcome the pageToken != "" checks above exist to prevent.
		//
		// Unlike those checks, this has no self-healing path: the SDK's persisted page
		// token means every retry of this same sync run arrives with the same stale
		// token and re-hits this branch, since there's nothing here that can reconstruct
		// the lost state. Recovery is operator-driven — start a fresh full sync (a new
		// run gets pageToken == "" and rebuilds state.Queues from scratch) once the
		// session store is confirmed to persist across whatever caused this loss.
		return nil, nil, fmt.Errorf("baton-docusign: CLM workflow queue discovery state missing mid-scan (page token %q); start a fresh full sync once the session store is stable", pageToken)
	}
	if found && (state.ScanComplete || pageToken != state.NextExpectedInputToken) {
		return b.replayChunk(bag, state)
	}
	if state.Queues == nil {
		state.Queues = make(map[string]client.ClmWorkflowQueue)
	}

	members, nextMemberPageToken, allAnnos, err := b.client.ListMembers(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, nil, err
	}

	// chunkMembersByQueue accumulates only THIS chunk's contribution to each queue's
	// membership — merged into that queue's own session key below instead of growing
	// state.Queues without bound (see this builder's doc for why).
	chunkMembersByQueue := make(map[string][]string)

	for _, member := range members {
		memberID := clmIDFromHref(member.Href)
		if memberID == "" {
			// Symmetric with the empty-queueID guard below: an empty ID would call
			// GetMemberWorkflowQueues(ctx, "") -> GET .../members//workflowqueues,
			// which 404s and — on the pre-success path — counts toward
			// clmWorkflowQueueUnavailableThreshold, so a handful of malformed members
			// early in scan order could hard-fail the sync and misreport it as "CLM
			// unavailable" instead of "found members with no usable ID." Own counter,
			// not the shared SkippedMembers below: sampling on a shared counter would
			// let this failure class go unlogged entirely if enough of the other kind
			// happened first.
			state.SkippedMembersNoID++
			if n := state.SkippedMembersNoID; n == 1 || n == 10 || n == 100 || n%1000 == 0 {
				ctxzap.Extract(ctx).Debug("baton-docusign: CLM member has an empty Href, skipping",
					zap.String("member_username", member.UserName), zap.Int("total_occurrences", n))
			}
			continue
		}
		queues, queueAnnos, err := b.client.GetMemberWorkflowQueues(ctx, memberID)
		if err != nil {
			if isOptInFeatureUnavailableError(err) {
				if !state.SucceededAtLeastOnce {
					// Nothing has proven this endpoint works for this account yet.
					// DocuSign's own CLM API docs (Response and Error Codes) confirm
					// NotFound isn't unique to "this object doesn't exist" — it's the
					// SAME response CLM returns for "this exists but you don't have
					// access rights", specifically so a 403 never leaks whether the
					// object exists ("If the user does not have permissions to see
					// the object or the object does not exist, a 404 response code is
					// returned"). So a NotFound here is just as plausible a signal
					// that workflow queues aren't available for this whole account as
					// PermissionDenied/Unauthenticated are — treat it the same way.
					//
					// But escalating on a SINGLE failure reintroduces a different
					// false-deletion race: a member that was genuinely deleted between
					// ListMembers and this call (the case the isolated-NotFound skip
					// below exists for) would wipe the whole resource type if it just
					// happens to be first in scan order. A systemic failure (the
					// endpoint disabled for this account) 404s/403s on EVERY member,
					// while an isolated deletion race hits exactly one — so require a
					// few consecutive failures (spanning as many chunks as it takes)
					// before concluding "systemic", capping wasted requests against an
					// already rate-limited endpoint without letting one unlucky
					// ordering fail the sync. Once concluded, fail loud rather than
					// skip gracefully: clm_workflow_queue is OptInRequired, and C1's
					// opt-in toggle doesn't check the account can actually use it first
					// — an account that opted in but can't reach this endpoint is a
					// misconfiguration to surface, not a state to tolerate silently.
					state.ConsecutiveUnavailableFailures++
					if state.ConsecutiveUnavailableFailures >= clmWorkflowQueueUnavailableThreshold {
						return nil, nil, fmt.Errorf("baton-docusign: CLM workflow queues unavailable after %d consecutive member failures: %w",
							state.ConsecutiveUnavailableFailures, err)
					}
					// Below the threshold: same visibility as the post-success
					// isolated-NotFound skip below — this member's queue membership
					// (if any) is silently missing from this sync otherwise.
					state.SkippedMembers++
					if n := state.SkippedMembers; n == 1 || n == 10 || n == 100 || n%1000 == 0 {
						ctxzap.Extract(ctx).Debug("baton-docusign: failed to get CLM workflow queues for member, skipping",
							zap.String("member_id", memberID), zap.Int("total_occurrences", n), zap.Error(err))
					}
					continue
				}
				if status.Code(err) == codes.NotFound {
					// Once at least one call has already succeeded, this endpoint is
					// clearly available for this account, so a NotFound from here on
					// is far more likely the isolated "member deleted between
					// ListMembers and this call" case than a systemic one — skip just
					// this member and keep scanning.
					state.SkippedMembers++
					if n := state.SkippedMembers; n == 1 || n == 10 || n == 100 || n%1000 == 0 {
						ctxzap.Extract(ctx).Debug("baton-docusign: CLM member not found while scanning workflow queues, skipping",
							zap.String("member_id", memberID), zap.Int("total_occurrences", n), zap.Error(err))
					}
					continue
				}
				// A non-NotFound tolerated code (PermissionDenied/Unauthenticated/
				// FailedPrecondition) after other members already succeeded is a
				// real, isolated problem (an expiring token, a scope revoked
				// mid-scan), not an unavailability signal — failing loud beats
				// discarding every already-discovered queue as if the whole feature
				// were unavailable.
			}
			return nil, nil, fmt.Errorf("baton-docusign: getting workflow queues for CLM member %s: %w", memberID, err)
		}
		state.SucceededAtLeastOnce = true
		allAnnos = append(allAnnos, queueAnnos...)

		for _, q := range queues {
			queueID := clmIDFromHref(q.Href)
			if queueID == "" {
				state.SkippedQueues++
				if n := state.SkippedQueues; n == 1 || n == 10 || n == 100 || n%1000 == 0 {
					ctxzap.Extract(ctx).Debug("baton-docusign: CLM workflow queue has an empty Href, skipping",
						zap.String("member_id", memberID), zap.String("queue_name", q.Name), zap.Int("total_occurrences", n))
				}
				continue
			}
			if _, ok := state.Queues[queueID]; !ok {
				state.Queues[queueID] = q
			}
			chunkMembersByQueue[queueID] = append(chunkMembersByQueue[queueID], memberID)
		}
	}

	// Merge this chunk's contribution into each touched queue's own session key rather
	// than growing a single all-queues blob every chunk — see this builder's doc for why.
	if len(chunkMembersByQueue) > 0 {
		keys := make([]string, 0, len(chunkMembersByQueue))
		for queueID := range chunkMembersByQueue {
			keys = append(keys, clmSessionKeyQueueMembers(queueID))
		}
		existing, err := session.GetManyJSON[[]string](ctx, attr.Session, keys)
		if err != nil {
			if pageToken != "" {
				// state.SucceededAtLeastOnce is always true by the time we reach this
				// block (populating chunkMembersByQueue requires a prior successful
				// GetMemberWorkflowQueues call), so it can't distinguish "real data exists
				// only in this in-memory chunk" from "an earlier chunk already durably
				// persisted real data" — a non-empty incoming page token is what actually
				// proves the latter. Losing an earlier chunk's persisted membership here
				// would make this the last (and only) response the SDK sees for this
				// resource type — an authoritative empty result that reads as every
				// previously synced clm_workflow_queue and its grants having been deleted.
				// Propagate the error instead: the SDK preserves the last-known-good sync
				// rather than accepting a lossy one.
				return nil, nil, fmt.Errorf("baton-docusign: failed to read cached CLM workflow queue membership: %w", err)
			}
			// First chunk: nothing has been durably persisted yet, so this in-memory
			// chunk's data is all that's at risk — same opt-in-session-store reasoning as
			// the discovery-state read above.
			ctxzap.Extract(ctx).Debug("baton-docusign: failed to read cached CLM workflow queue membership, skipping clm_workflow_queue sync", zap.Error(err))
			return nil, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
		}
		membersByKey := make(map[string][]string, len(chunkMembersByQueue))
		for queueID, newMembers := range chunkMembersByQueue {
			key := clmSessionKeyQueueMembers(queueID)
			// Dedup: a resumed sync can re-issue an already-processed chunk.
			seen := make(map[string]struct{}, len(existing[key])+len(newMembers))
			merged := existing[key]
			for _, m := range merged {
				seen[m] = struct{}{}
			}
			for _, m := range newMembers {
				if _, ok := seen[m]; ok {
					continue
				}
				seen[m] = struct{}{}
				merged = append(merged, m)
			}
			membersByKey[key] = merged
		}
		if err := session.SetManyJSON(ctx, attr.Session, membersByKey); err != nil {
			if pageToken != "" {
				// Same reasoning as the read above: state.SucceededAtLeastOnce can't tell
				// "this chunk" apart from "an earlier, already-persisted chunk" here, so
				// use the incoming page token instead. This chunk's membership updates
				// would be silently dropped, and a graceful zero-resource response here
				// reads as everything already discovered having been deleted. Fail loud
				// instead of accepting that outcome.
				return nil, nil, fmt.Errorf("baton-docusign: failed to cache CLM workflow queue membership: %w", err)
			}
			// First chunk: nothing has been durably persisted yet.
			ctxzap.Extract(ctx).Debug("baton-docusign: failed to cache CLM workflow queue membership, skipping clm_workflow_queue sync", zap.Error(err))
			return nil, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
		}
	}

	state.NextExpectedInputToken = nextMemberPageToken
	state.ScanComplete = nextMemberPageToken == ""
	if err := session.SetJSON(ctx, attr.Session, clmSessionKeyWorkflowQueueDiscoveryState, state); err != nil {
		if pageToken != "" {
			// Same reasoning as the other three session-store failure sites above — and
			// deliberately the SAME predicate, not state.SucceededAtLeastOnce: that field
			// answers "did a member scan succeed", not "did an earlier chunk already
			// prove this session store works", and the two diverge whenever every member
			// scanned so far has failed below the escalation threshold. A non-empty
			// incoming page token means chunk 1 already wrote successfully (that's the
			// only way to get here with one), so a failure now is a genuine regression in
			// an otherwise-working store, not the first-chunk "maybe never wired at all"
			// case. Fail loud so the SDK preserves the last-known-good sync instead of
			// accepting a lossy empty result.
			return nil, nil, fmt.Errorf("baton-docusign: failed to cache CLM workflow queue discovery progress: %w", err)
		}
		// The session store is opt-in end-to-end: WithSessionStoreEnabled (main.go)
		// only tells the SDK to accept a store connection — whether one actually
		// exists still depends on the parent process wiring a listen port, and it
		// falls back to NoOpSessionStore (every Set call fails) whenever it doesn't.
		// This resource type's Grants() cannot function without it, but that's not
		// true of the rest of the sync — a hard error here would fail every other
		// resource type too. Skip gracefully instead, same as an unavailable CLM
		// subscription.
		ctxzap.Extract(ctx).Debug("baton-docusign: failed to cache CLM workflow queue discovery progress, skipping clm_workflow_queue sync", zap.Error(err))
		return nil, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
	}

	if nextMemberPageToken != "" {
		outToken, err := bag.NextToken(nextMemberPageToken)
		if err != nil {
			return nil, nil, err
		}
		return nil, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos), NextPageToken: outToken}, nil
	}

	// Last member page: the queue set is only guaranteed complete now (see this
	// builder's doc) — every queue's membership was already persisted incrementally
	// above, so just emit every discovered queue as a resource.
	resources := make([]*v2.Resource, 0, len(state.Queues))
	for _, q := range state.Queues {
		queueResource, err := parseIntoClmWorkflowQueueResource(&q)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, queueResource)
	}

	return resources, &rs.SyncOpResults{Annotations: dedupeRateLimitAnnotations(allAnnos)}, nil
}

// replayChunk handles a resumed sync arriving with an input token that isn't the one
// this state expects next (see NextExpectedInputToken's doc). The per-queue membership
// merge is dedup-safe against a replay, but the escalation counters aren't, so this
// resumes from the persisted frontier instead of re-running the per-member scan and its
// counter updates.
func (b *clmWorkflowQueueBuilder) replayChunk(bag *pagination.Bag, state clmWorkflowQueueDiscoveryState) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if state.NextExpectedInputToken != "" {
		outToken, err := bag.NextToken(state.NextExpectedInputToken)
		if err != nil {
			return nil, nil, err
		}
		return nil, &rs.SyncOpResults{NextPageToken: outToken}, nil
	}
	resources := make([]*v2.Resource, 0, len(state.Queues))
	for _, q := range state.Queues {
		queueResource, err := parseIntoClmWorkflowQueueResource(&q)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, queueResource)
	}
	return resources, &rs.SyncOpResults{}, nil
}

// dedupeRateLimitAnnotations keeps every non-rate-limit annotation as-is but collapses
// all RateLimitDescription entries down to the last one — List() appends one
// GetMemberWorkflowQueues annotation set per member processed in its current chunk, so a
// response can carry several near-identical rate-limit snapshots; only the most recent
// one is meaningful. Chunking (one ListMembers page per call) already bounds this to one
// page's worth of members rather than the whole account, but a page can still be in the
// hundreds, so this stays worth doing.
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
