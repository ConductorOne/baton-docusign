package connector

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/session"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
)

// fakeSessionStore is a minimal in-memory sessions.SessionStore for tests — the real
// implementations either require a live gRPC session server or otter cache wiring not
// worth pulling into a unit test. Ignores the SyncID/prefix bag entirely: a single test
// only ever needs one sync's worth of isolation.
type fakeSessionStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

var _ sessions.SessionStore = (*fakeSessionStore)(nil)

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{data: make(map[string][]byte)}
}

func (f *fakeSessionStore) Get(_ context.Context, key string, _ ...sessions.SessionStoreOption) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	return v, ok, nil
}

// GetMany's second return is for keys this call couldn't get to and wants retried —
// session.UnrollGetMany loops passing it straight back in as the next call's key list,
// erroring if it ever stops shrinking. It is NOT "missing/never-written keys": those
// simply aren't present in the returned map, matching every real SessionStore
// implementation (e.g. dotc1z's SQL "WHERE key IN (...)" naturally omits absent rows).
// This fake never has a reason to ask for a retry, so it always returns nil here.
func (f *fakeSessionStore) GetMany(_ context.Context, keys []string, _ ...sessions.SessionStoreOption) (map[string][]byte, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte)
	for _, k := range keys {
		if v, ok := f.data[k]; ok {
			out[k] = v
		}
	}
	return out, nil, nil
}

func (f *fakeSessionStore) Set(_ context.Context, key string, value []byte, _ ...sessions.SessionStoreOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = value
	return nil
}

func (f *fakeSessionStore) SetMany(_ context.Context, values map[string][]byte, _ ...sessions.SessionStoreOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range values {
		f.data[k] = v
	}
	return nil
}

func (f *fakeSessionStore) Delete(_ context.Context, key string, _ ...sessions.SessionStoreOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func (f *fakeSessionStore) Clear(_ context.Context, _ ...sessions.SessionStoreOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = make(map[string][]byte)
	return nil
}

func (f *fakeSessionStore) GetAll(_ context.Context, _ string, _ ...sessions.SessionStoreOption) (map[string][]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	return out, "", nil
}

// failingSessionStore wraps fakeSessionStore but every Set/SetMany call fails — isolates
// the write-failure path (e.g. a value that exceeds the store's size limit) from reads,
// which still succeed via the embedded fakeSessionStore.
type failingSessionStore struct {
	*fakeSessionStore
}

func (f *failingSessionStore) Set(_ context.Context, _ string, _ []byte, _ ...sessions.SessionStoreOption) error {
	return errClmSessionStoreDisabledForTest
}

func (f *failingSessionStore) SetMany(_ context.Context, _ map[string][]byte, _ ...sessions.SessionStoreOption) error {
	return errClmSessionStoreDisabledForTest
}

// readFailingSessionStore wraps fakeSessionStore but every Get/GetMany call fails —
// stands in for the SDK's real NoOpSessionStore (returned whenever the parent process
// hasn't wired a session-store listen port; see session.NoOpSessionStore), whose reads
// fail the exact same way as its writes.
type readFailingSessionStore struct {
	*fakeSessionStore
}

func (f *readFailingSessionStore) Get(_ context.Context, _ string, _ ...sessions.SessionStoreOption) ([]byte, bool, error) {
	return nil, false, errClmSessionStoreDisabledForTest
}

func (f *readFailingSessionStore) GetMany(_ context.Context, _ []string, _ ...sessions.SessionStoreOption) (map[string][]byte, []string, error) {
	return nil, nil, errClmSessionStoreDisabledForTest
}

var errClmSessionStoreDisabledForTest = errors.New("session store disabled (test double)")

// TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnSessionStoreWriteFailure confirms
// List() degrades to a graceful skip (not a hard error) when it can't write to the
// session cache — e.g. because the parent process didn't wire a session-store listen
// port and the SDK fell back to NoOpSessionStore. A hard error here would fail the
// entire sync, not just this resource type, since every other CLM builder's List() also
// runs unconditionally in the same sync.
func TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnSessionStoreWriteFailure(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: &failingSessionStore{fakeSessionStore: newFakeSessionStore()}})
	if err != nil {
		t.Fatalf("expected a session-store write failure to be tolerated, not an error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when the session store can't be written to, got %d", len(resources))
	}
	if res == nil {
		t.Errorf("expected a non-nil SyncOpResults, got %+v", res)
	}
}

// TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnSessionStoreReadFailure confirms
// List() degrades to a graceful skip (not a hard error) when it can't read its
// discovery state from the session cache — the same NoOpSessionStore fallback as the
// write-failure case above, just hit on the read that now happens first in every call.
func TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnSessionStoreReadFailure(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: &readFailingSessionStore{fakeSessionStore: newFakeSessionStore()}})
	if err != nil {
		t.Fatalf("expected a session-store read failure to be tolerated, not an error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when the session store can't be read from, got %d", len(resources))
	}
	if res == nil {
		t.Errorf("expected a non-nil SyncOpResults, got %+v", res)
	}
}

// TestClmWorkflowQueueBuilder_List_FailsLoudlyOnSessionStoreFailureAfterFirstChunk is the
// regression test for the false-deletion bug the pageToken != "" gating (see List's
// discovery-state read) exists to prevent: unlike the two graceful-skip tests above
// (both hit on the very first chunk, before anything has been persisted), a
// session-store read failure on a LATER chunk — after an earlier chunk already durably
// wrote real queue data — must fail the sync instead of reporting zero resources, which
// would read as every already-discovered queue having been deleted.
func TestClmWorkflowQueueBuilder_List_FailsLoudlyOnSessionStoreFailureAfterFirstChunk(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	store := newFakeSessionStore()

	_, syncRes, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: store, PageToken: pagination.Token{Size: 1}})
	if err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	if syncRes.NextPageToken == "" {
		t.Fatalf("expected more than one seeded member so chunk 1 doesn't already finish the scan")
	}

	_, _, err = b.List(ctx, nil, rs.SyncOpAttrs{
		Session:   &readFailingSessionStore{fakeSessionStore: store},
		PageToken: pagination.Token{Size: 1, Token: syncRes.NextPageToken},
	})
	if err == nil {
		t.Fatal("expected a later-chunk session-store read failure to fail loudly, not skip " +
			"gracefully — an earlier chunk already persisted real queue data that a graceful " +
			"zero-resource response would read as deleted")
	}
}

func TestClmWorkflowQueueBuilder_List_FailsWhenClmUnavailable(t *testing.T) {
	// clm_workflow_queue is OptInRequired, and C1's opt-in toggle doesn't check the
	// account can actually use it first — see List()'s escalation branch in
	// clm_workflow_queues.go for the full rationale. List() must fail loudly here
	// rather than silently succeed with zero resources.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmWorkflowQueueBuilder(badClient)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err == nil {
		t.Fatal("expected List to fail when CLM is unavailable, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

func TestClmWorkflowQueueBuilder_StaticEntitlements(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	ents, _, err := b.StaticEntitlements(ctx, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("StaticEntitlements: %v", err)
	}
	if len(ents) != 1 || ents[0].Slug != entitlementClmWorkflowQueueMember {
		t.Fatalf("expected a single %q entitlement, got %+v", entitlementClmWorkflowQueueMember, ents)
	}
}

// TestClmWorkflowQueueBuilder_List_DiscoversQueuesViaMemberScan is the core regression
// test for this builder's whole design: there is no list-all endpoint for workflow
// queues (see clmWorkflowQueueBuilder's doc), so List() has to discover the distinct
// set by scanning every member's own workflow-queue membership and deduping. The seed
// data (clmtest/seed.go) puts member-alice in one queue and member-bob in two, with one
// queue (Onboarding) shared between them — this confirms both the discovery and the
// dedup-by-ID across members.
func TestClmWorkflowQueueBuilder_List_DiscoversQueuesViaMemberScan(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil SyncOpResults")
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 distinct workflow queues (Onboarding, Escalations), got %d: %+v", len(resources), resources)
	}

	names := make(map[string]bool)
	for _, r := range resources {
		names[r.DisplayName] = true
	}
	if !names["Onboarding"] || !names["Escalations"] {
		t.Errorf("expected both Onboarding and Escalations among the discovered queues, got %v", names)
	}
}

// TestClmWorkflowQueueBuilder_List_SkipsMemberWithEmptyHref confirms the empty-memberID
// guard: a malformed member with no Href must not reach GetMemberWorkflowQueues at all
// (which would 404 and, on the pre-success path, count toward
// clmWorkflowQueueUnavailableThreshold), must not disturb discovery of the other
// members' real queues, and must not touch the escalation counter — it's a data-quality
// skip, not an unavailability signal.
func TestClmWorkflowQueueBuilder_List_SkipsMemberWithEmptyHref(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.AddMemberWithoutHref("member-no-href")
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("expected a member with an empty Href to be skipped, got error: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected the other members' 2 queues to still be discovered, got %d: %+v", len(resources), resources)
	}
	if got := srv.MemberWorkflowQueuesRequestCount(); got != 6 {
		t.Errorf("expected exactly 6 GetMemberWorkflowQueues calls (the 6 real seeded members, not the malformed one), got %d", got)
	}
}

// TestClmWorkflowQueueBuilder_List_ToleratesNotFoundMidScan confirms a single member
// 404ing (deleted between ListMembers and this call) is skipped, not a sync-wide
// failure, once at least one other member has already proven the endpoint works —
// member-carol is scanned after member-alice (who succeeds and contributes a queue),
// so this exercises the "isolated NotFound" branch specifically, not the "nothing has
// succeeded yet" escalation
// TestClmWorkflowQueueBuilder_List_FailsAfterConsecutiveFailures covers.
func TestClmWorkflowQueueBuilder_List_ToleratesNotFoundMidScan(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	// member-carol is a real seeded member (clmtest/seed.go) with zero queues of its
	// own — forcing 404 here confirms the skip doesn't disturb discovery of the other
	// members' queues (Onboarding/Escalations), not just that the scan doesn't crash.
	srv.ForceMemberWorkflowQueuesStatus("member-carol", 404)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("expected a 404 on one member to be tolerated, got error: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected the other members' 2 queues to still be discovered, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_List_ToleratesBelowThresholdFailures is a regression
// test: escalating to the account-wide-unavailability skip on a SINGLE tolerated
// failure (the previous behavior) reintroduced the exact false-deletion race the
// isolated-NotFound skip exists to avoid — a member genuinely deleted between
// ListMembers and this call would wipe the whole resource type if it happened to be
// first in scan order. Forcing a 404 on only member-alice (first in scan order, below
// clmWorkflowQueueUnavailableThreshold) must NOT escalate: member-bob's real queues
// still get discovered normally.
func TestClmWorkflowQueueBuilder_List_ToleratesBelowThresholdFailures(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 404)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("expected a single below-threshold failure to be tolerated, got error: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected member-bob's 2 real queues to still be discovered, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_List_FailsAfterConsecutiveFailures confirms the
// account-wide-unavailability escalation still fires once
// clmWorkflowQueueUnavailableThreshold consecutive members fail with nothing
// discovered yet — member-alice, member-bob, and member-carol are first in scan order
// (clmtest/seed.go's memberOrder), so forcing all three to fail reaches the threshold
// before any of them can succeed. Once reached, List() fails loud (see
// TestClmWorkflowQueueBuilder_List_FailsWhenClmUnavailable's rationale) rather than
// tolerating it.
func TestClmWorkflowQueueBuilder_List_FailsAfterConsecutiveFailures(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-bob", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-carol", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err == nil {
		t.Fatalf("expected List to fail after %d consecutive failures with nothing discovered yet, got nil error", clmWorkflowQueueUnavailableThreshold)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_List_FailsLoudlyWhenLaterMemberDenied is a regression
// test: List() previously escalated ANY isOptInFeatureUnavailableError to "CLM
// unavailable, return zero resources",
// regardless of scan position — so a PermissionDenied on member N (a token expiring or
// a scope revoked mid-scan) after earlier members had already contributed real queues
// would silently discard every already-discovered queue as if the whole feature were
// unavailable, the same false-deletion risk ListMembers' own memberPageToken == ""
// narrowing exists to avoid. member-bob is scanned after member-alice (whose Onboarding
// queue is already in membership by the time bob's call fails), so this must now fail
// loud instead of returning zero resources with a nil error.
func TestClmWorkflowQueueBuilder_List_FailsLoudlyWhenLaterMemberDenied(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-bob", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err == nil {
		t.Fatal("expected a PermissionDenied after queues were already discovered to fail loudly, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_List_ZeroQueueSuccessCountsAsSucceeded is a regression
// test for succeededAtLeastOnce's exact semantics: it must be set by ANY successful
// GetMemberWorkflowQueues call, including one that finds zero queues, not just one that
// contributes to membership. member-alice and member-bob (the only two seeded members
// with real queues) are both forced to fail — below clmWorkflowQueueUnavailableThreshold,
// so neither escalates — and member-carol (zero queues, clmtest/seed.go) succeeds next,
// which must count as proof the endpoint works. member-dave failing afterward must then
// fail loud, not be treated as still-pre-success.
func TestClmWorkflowQueueBuilder_List_ZeroQueueSuccessCountsAsSucceeded(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-bob", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-dave", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err == nil {
		t.Fatal("expected member-dave's failure (after member-carol's zero-queue success) to fail loudly, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_Grants_ReadsFromCache confirms the other half of this
// builder's design: Grants() must NOT re-scan every member per queue (that would turn
// one O(members) traversal into O(queues * members) — see the builder's doc) — it reads
// the member list List() already cached for this exact queue.
func TestClmWorkflowQueueBuilder_Grants_ReadsFromCache(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore()}

	resources, _, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	byName := make(map[string]*v2.Resource)
	for _, r := range resources {
		byName[r.DisplayName] = r
	}

	grants, _, err := b.Grants(ctx, byName["Onboarding"], attr)
	if err != nil {
		t.Fatalf("Grants(Onboarding): %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("expected 2 members (alice, bob) in Onboarding, got %d: %+v", len(grants), grants)
	}

	grants, _, err = b.Grants(ctx, byName["Escalations"], attr)
	if err != nil {
		t.Fatalf("Grants(Escalations): %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected 1 member (bob) in Escalations, got %d: %+v", len(grants), grants)
	}
}

// TestClmWorkflowQueueBuilder_Grants_CacheMiss confirms Grants() fails loudly (not a
// silent zero-grants degrade, and not a fallback member re-scan) when the cache has
// nothing for a queue — e.g. if it's ever called without List() having populated it
// first. Zero grants would be indistinguishable from this queue's membership having
// been genuinely emptied out, which is worse than failing the sync.
func TestClmWorkflowQueueBuilder_Grants_CacheMiss(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	queueResource := &v2.Resource{Id: &v2.ResourceId{ResourceType: clmWorkflowQueueResourceType.Id, Resource: "queue-onboarding"}}
	grants, _, err := b.Grants(ctx, queueResource, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err == nil {
		t.Fatal("expected a cache miss to fail loudly, got nil error")
	}
	if len(grants) != 0 {
		t.Errorf("expected zero grants on a cache miss, got %d: %+v", len(grants), grants)
	}
}

// TestClmWorkflowQueueBuilder_List_ChunksAcrossPages is the core regression test for
// this builder's chunked design: List() must page one ListMembers page per call, like
// every other builder in this connector, instead of running the entire member scan
// inside a single call. Forcing PageSize 2 against the 6 seeded members (alice, bob,
// carol, dave, eve, frank) produces 3 ListMembers pages, so 3 separate List() calls are
// required.
// Every call but the last must return zero resources with a non-empty NextPageToken —
// the queue set can't be confirmed complete before the member scan is — and the final
// call must return the same 2 distinct queues (Onboarding, Escalations) the single-call
// design already proved correct in TestClmWorkflowQueueBuilder_List_DiscoversQueuesViaMemberScan.
func TestClmWorkflowQueueBuilder_List_ChunksAcrossPages(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore()}

	var resources []*v2.Resource
	pageToken := ""
	for i := 0; i < 10; i++ {
		attr.PageToken = pagination.Token{Size: 2, Token: pageToken}
		res, syncRes, err := b.List(ctx, nil, attr)
		if err != nil {
			t.Fatalf("List page %d: %v", i, err)
		}
		if syncRes.NextPageToken == "" {
			resources = res
			break
		}
		if len(res) != 0 {
			t.Fatalf("page %d: expected zero resources before the member scan completes, got %d: %+v", i, len(res), res)
		}
		pageToken = syncRes.NextPageToken
	}

	if len(resources) != 2 {
		t.Fatalf("expected 2 distinct workflow queues (Onboarding, Escalations) on the final page, got %d: %+v", len(resources), resources)
	}
	names := make(map[string]bool)
	for _, r := range resources {
		names[r.DisplayName] = true
	}
	if !names["Onboarding"] || !names["Escalations"] {
		t.Errorf("expected both Onboarding and Escalations among the discovered queues, got %v", names)
	}

	// Grants() must work off the same session store exactly as the single-call design.
	byName := make(map[string]*v2.Resource)
	for _, r := range resources {
		byName[r.DisplayName] = r
	}
	grants, _, err := b.Grants(ctx, byName["Onboarding"], attr)
	if err != nil {
		t.Fatalf("Grants(Onboarding): %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("expected 2 members (alice, bob) in Onboarding, got %d: %+v", len(grants), grants)
	}
}

// TestClmWorkflowQueueBuilder_List_MergesMembershipAcrossChunks is the regression test
// for the incremental per-queue session write: each chunk merges its own contribution
// into a queue's session key via a Get-then-append-then-Set round trip, and a wrong
// merge (e.g. overwriting instead of appending) would silently drop every earlier
// chunk's members. member-alice and member-bob are both in the Onboarding queue (see
// clmtest/seed.go) but PageSize 1 puts them in separate chunks, so this only passes if
// chunk 2's write actually preserves chunk 1's contribution.
func TestClmWorkflowQueueBuilder_List_MergesMembershipAcrossChunks(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore()}

	var resources []*v2.Resource
	pageToken := ""
	for i := 0; i < 10; i++ {
		attr.PageToken = pagination.Token{Size: 1, Token: pageToken}
		res, syncRes, err := b.List(ctx, nil, attr)
		if err != nil {
			t.Fatalf("List page %d: %v", i, err)
		}
		if syncRes.NextPageToken == "" {
			resources = res
			break
		}
		pageToken = syncRes.NextPageToken
	}

	byName := make(map[string]*v2.Resource)
	for _, r := range resources {
		byName[r.DisplayName] = r
	}
	grants, _, err := b.Grants(ctx, byName["Onboarding"], attr)
	if err != nil {
		t.Fatalf("Grants(Onboarding): %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("expected both alice (chunk 1) and bob (chunk 2) merged into Onboarding, got %d: %+v", len(grants), grants)
	}
}

// TestClmWorkflowQueueBuilder_List_ReplayedChunkDoesNotDuplicateMembership is a
// regression test: a resumed sync can re-issue a List() call with a PageToken whose
// chunk was already processed and merged. Re-running the first chunk (alice) must not
// double-count alice in Onboarding's cached membership.
func TestClmWorkflowQueueBuilder_List_ReplayedChunkDoesNotDuplicateMembership(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore(), PageToken: pagination.Token{Size: 1}}

	if _, _, err := b.List(ctx, nil, attr); err != nil {
		t.Fatalf("List (first run of chunk 1): %v", err)
	}
	if _, _, err := b.List(ctx, nil, attr); err != nil {
		t.Fatalf("List (replayed chunk 1): %v", err)
	}

	memberIDs, found, err := session.GetJSON[[]string](ctx, attr.Session, clmSessionKeyQueueMembers("queue-onboarding"))
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !found {
		t.Fatal("expected cached membership for queue-onboarding")
	}
	if len(memberIDs) != 1 || memberIDs[0] != "member-alice" {
		t.Errorf("expected exactly one alice entry after the replay, got %v", memberIDs)
	}
}

// TestClmWorkflowQueueBuilder_List_EscalationThresholdSpansChunks is a regression test
// for the specific risk chunking introduces: the escalation-threshold counters
// (clmWorkflowQueueDiscoveryState) must persist and keep accumulating ACROSS separate
// List() calls, not reset per chunk — otherwise 2 consecutive failures in one chunk
// followed by 1 more in the next chunk would never reach clmWorkflowQueueUnavailableThreshold
// (3), even though the same 3 consecutive failures in a single unchunked call already do
// (per TestClmWorkflowQueueBuilder_List_FailsAfterConsecutiveFailures). Forces
// alice and bob (chunk 1, PageSize 2) and carol (chunk 2) to all fail — the first chunk
// alone only sees 2 failures (below threshold, must NOT escalate yet), and the second
// chunk's first member pushes the running total to 3 and must escalate there.
func TestClmWorkflowQueueBuilder_List_EscalationThresholdSpansChunks(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-bob", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-carol", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore()}

	// Chunk 1: alice, bob — 2 failures, below threshold, must continue (non-empty
	// NextPageToken) with zero resources, not escalate yet.
	attr.PageToken = pagination.Token{Size: 2, Token: ""}
	resources, syncRes, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("chunk 1: expected 2 below-threshold failures to be tolerated, got error: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("chunk 1: expected zero resources, got %d: %+v", len(resources), resources)
	}
	if syncRes.NextPageToken == "" {
		t.Fatal("chunk 1: expected a non-empty NextPageToken — the member scan isn't done and shouldn't have escalated yet")
	}

	// Chunk 2: carol is first — this is the 3rd CONSECUTIVE failure counting the two
	// from chunk 1, so it must escalate here, before ever reaching dave — and now fails
	// loud rather than skipping gracefully.
	attr.PageToken = pagination.Token{Size: 2, Token: syncRes.NextPageToken}
	resources, _, err = b.List(ctx, nil, attr)
	if err == nil {
		t.Fatal("chunk 2: expected the threshold-crossing failure to fail loud, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("chunk 2: expected zero resources on a hard failure, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_List_ReplayedChunkDoesNotDoubleCountFailures is a
// regression test: a resumed sync can re-issue a List() call with the same PageToken as
// a chunk already applied. Replaying a below-threshold chunk must not double-count its
// failures toward ConsecutiveUnavailableFailures and falsely escalate.
func TestClmWorkflowQueueBuilder_List_ReplayedChunkDoesNotDoubleCountFailures(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-bob", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore(), PageToken: pagination.Token{Size: 2, Token: ""}}

	_, syncRes, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	if syncRes.NextPageToken == "" {
		t.Fatal("chunk 1: expected a non-empty NextPageToken — 2 failures is below the threshold")
	}

	// Replay chunk 1 with the exact same input token.
	resources, syncRes, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("replayed chunk 1: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("replayed chunk 1: expected zero resources, got %d: %+v", len(resources), resources)
	}
	if syncRes.NextPageToken == "" {
		t.Fatal("replayed chunk 1: expected a non-empty NextPageToken — must not have escalated from double-counted failures")
	}
}

// TestClmWorkflowQueueBuilder_List_ReplayedRollbackByMoreThanOneChunk is a regression
// test: a resume can roll back more than one chunk, replaying an input token that isn't
// the immediately preceding one. Replaying chunk 1's token after chunk 2 already applied
// must resume from the current frontier (chunk 2's own NextPageToken), not re-run either
// chunk and double-count their failures.
func TestClmWorkflowQueueBuilder_List_ReplayedRollbackByMoreThanOneChunk(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	srv.ForceMemberWorkflowQueuesStatus("member-bob", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore(), PageToken: pagination.Token{Size: 1, Token: ""}}

	_, syncRes, err := b.List(ctx, nil, attr) // chunk 1: alice fails (count=1)
	if err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	attr.PageToken.Token = syncRes.NextPageToken

	_, syncRes, err = b.List(ctx, nil, attr) // chunk 2: bob fails (count=2)
	if err != nil {
		t.Fatalf("chunk 2: %v", err)
	}
	if syncRes.NextPageToken == "" {
		t.Fatal("chunk 2: expected a non-empty NextPageToken — 2 failures is below the threshold")
	}
	frontier := syncRes.NextPageToken

	// Replay chunk 1's original token — two chunks stale, not just one.
	attr.PageToken.Token = ""
	resources, syncRes, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("rolled-back replay: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("rolled-back replay: expected zero resources, got %d: %+v", len(resources), resources)
	}
	if syncRes.NextPageToken != frontier {
		t.Errorf("rolled-back replay: expected to resume from the frontier %q, got %q", frontier, syncRes.NextPageToken)
	}
}

// TestClmWorkflowQueueBuilder_List_ReplayOfSinglePageScanIsDetected is a regression
// test: a scan that completes in a single page has NextExpectedInputToken == "" both
// before the first call and after the scan finishes, so token comparison alone can't
// tell a replay of that one chunk from a genuine first call. Without ScanComplete, a
// replay here would re-process member-alice's tolerated failure AFTER
// SucceededAtLeastOnce is already true, hitting the "fails loud" branch instead of the
// first-pass escalation path — turning a harmless replay into a hard sync failure.
func TestClmWorkflowQueueBuilder_List_ReplayOfSinglePageScanIsDetected(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()
	attr := rs.SyncOpAttrs{Session: newFakeSessionStore()}

	_, syncRes, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if syncRes.NextPageToken != "" {
		t.Fatal("first call: expected the scan to complete in a single page")
	}

	resources, _, err := b.List(ctx, nil, attr)
	if err != nil {
		t.Fatalf("replay of the single-page scan: %v", err)
	}
	if len(resources) != 2 {
		t.Errorf("replay: expected the same 2 queues as the first call, got %d: %+v", len(resources), resources)
	}
}
