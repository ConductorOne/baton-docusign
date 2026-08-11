package connector

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
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

func (f *fakeSessionStore) GetMany(_ context.Context, keys []string, _ ...sessions.SessionStoreOption) (map[string][]byte, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte)
	var missing []string
	for _, k := range keys {
		if v, ok := f.data[k]; ok {
			out[k] = v
		} else {
			missing = append(missing, k)
		}
	}
	return out, missing, nil
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

// failingSessionStore wraps fakeSessionStore but every Set/SetMany call fails — stands
// in for the SDK's real NoOpSessionStore (returned whenever the parent process hasn't
// wired a session-store listen port; see session.NoOpSessionStore), which every write
// to it fails the exact same way.
type failingSessionStore struct {
	*fakeSessionStore
}

func (f *failingSessionStore) Set(_ context.Context, _ string, _ []byte, _ ...sessions.SessionStoreOption) error {
	return errClmSessionStoreDisabledForTest
}

func (f *failingSessionStore) SetMany(_ context.Context, _ map[string][]byte, _ ...sessions.SessionStoreOption) error {
	return errClmSessionStoreDisabledForTest
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

func TestClmWorkflowQueueBuilder_List_SkipsGracefullyWhenClmUnavailable(t *testing.T) {
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmWorkflowQueueBuilder(badClient)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("expected List to tolerate an unavailable CLM account and skip gracefully, got error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when CLM is unavailable, got %d", len(resources))
	}
	if res == nil {
		t.Errorf("expected a non-nil SyncOpResults, got %+v", res)
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

// TestClmWorkflowQueueBuilder_List_ToleratesNotFoundMidScan confirms a single member
// 404ing (deleted between ListMembers and this call) is skipped, not a sync-wide
// failure, once at least one other member has already proven the endpoint works —
// member-carol is scanned after member-alice (who succeeds and contributes a queue),
// so this exercises the "isolated NotFound" branch specifically, not the "nothing has
// succeeded yet" escalation TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnFirst404
// covers.
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

// TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnFirst404 is a regression test:
// DocuSign's own CLM API docs (Response and Error Codes) confirm NotFound is the SAME
// response CLM returns for "no access rights" as for "object doesn't exist" — a 403
// never leaks whether the object exists. So a 404 on the very first member (nothing
// discovered yet) must escalate to the account-wide-unavailability skip exactly like
// 401/403 do, not be treated as an isolated "this one member is gone" case — otherwise
// an account systemically lacking workflow-queues access would pay one wasted request
// per member, every sync, before ever concluding that (CXP-704).
func TestClmWorkflowQueueBuilder_List_SkipsGracefullyOnFirst404(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 404)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("expected List to tolerate a 404 on the first member with nothing discovered yet, got error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources, got %d: %+v", len(resources), resources)
	}
	if res == nil {
		t.Errorf("expected a non-nil SyncOpResults, got %+v", res)
	}
}

// TestClmWorkflowQueueBuilder_List_SkipsGracefullyWhenFirstMemberDenied confirms the
// account-wide-unavailability escalation (errClmWorkflowQueuesUnavailable) still fires
// when nothing has been discovered yet — member-alice is first in scan order
// (clmtest/seed.go's memberOrder) and forcing PermissionDenied there means zero queues
// exist in membership at the point of failure.
func TestClmWorkflowQueueBuilder_List_SkipsGracefullyWhenFirstMemberDenied(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore()})
	if err != nil {
		t.Fatalf("expected List to tolerate a PermissionDenied with nothing discovered yet, got error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources, got %d: %+v", len(resources), resources)
	}
	if res == nil {
		t.Errorf("expected a non-nil SyncOpResults, got %+v", res)
	}
}

// TestClmWorkflowQueueBuilder_List_FailsLoudlyWhenLaterMemberDenied is a regression
// test: discoverClmWorkflowQueueMembership previously escalated ANY
// isOptInFeatureUnavailableError to "CLM unavailable, return zero resources",
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
