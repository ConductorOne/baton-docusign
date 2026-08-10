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
