package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/status"
)

func TestClmRoleBuilder_List(t *testing.T) {
	// The role set itself isn't backed by an API call, but List() now checks CLM
	// availability via EnsureClmReady first (see clm_roles.go), so it needs a working
	// mock client to reach the "CLM is available" branch.
	_, c := clmtest.NewServer(t)
	b := newClmRoleBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil SyncOpResults")
	}
	if len(resources) != len(client.ClmRoles) {
		t.Fatalf("expected %d fixed CLM roles, got %d", len(client.ClmRoles), len(resources))
	}
	for i, r := range resources {
		if r.Id.Resource != client.ClmRoles[i].Name {
			t.Errorf("resource %d: expected ID %q, got %q", i, client.ClmRoles[i].Name, r.Id.Resource)
		}
	}
}

func TestClmRoleBuilder_List_SkipsGracefullyWhenClmUnavailable(t *testing.T) {
	// See clm_members_test.go's identical test for the full rationale. Before List()
	// gated on EnsureClmReady, clm_roles.go made no API call at all, so this case
	// couldn't happen — the 5 fixed roles synced unconditionally even without CLM access.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmRoleBuilder(badClient)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{})
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

// TestClmRoleBuilder_List_FailsLoudlyOnTransientDiscoveryFailure is a regression test:
// ensureClmInitialized wraps EVERY discovery-call failure as a clmDiscoveryError,
// including transient infrastructure failures (5xx, rate limits), not just the 4 codes
// isOptInFeatureUnavailableError tolerates. Gating solely on
// client.IsClmDiscoveryError(err) — without also requiring
// isOptInFeatureUnavailableError(err) — would make clm_role silently skip on a 503 that
// every other CLM builder correctly treats as a loud failure.
func TestClmRoleBuilder_List_FailsLoudlyOnTransientDiscoveryFailure(t *testing.T) {
	s, c := clmtest.NewServer(t)
	s.ForceClmDiscoveryStatus(503)
	b := newClmRoleBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected a transient discovery failure (503) to fail loudly, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

// TestClmRoleBuilder_List_FailsLoudlyOnNonDiscoveryTolerableError is a regression test
// for the OTHER half of List()'s "requires both conditions" gate: a tolerated code
// (Unauthenticated here) that comes from eSignature's own ensureInitialized, not CLM
// account discovery, must still fail loud. Without the client.IsClmDiscoveryError(err)
// conjunct, this would be silently mistaken for "no CLM subscription" — the case
// clm_roles.go's own doc comment names first. Deleting that conjunct alone would leave
// TestClmRoleBuilder_List_SkipsGracefullyWhenClmUnavailable and
// TestClmRoleBuilder_List_FailsLoudlyOnTransientDiscoveryFailure both green, since
// neither exercises a tolerated code from this specific source.
func TestClmRoleBuilder_List_FailsLoudlyOnNonDiscoveryTolerableError(t *testing.T) {
	s, c := clmtest.NewServer(t)
	s.ForceUserInfoStatus(401)
	b := newClmRoleBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected a tolerated code from a non-discovery source to fail loudly, got nil error")
	}
	// Pins the two preconditions that make this a regression test for the
	// IsClmDiscoveryError conjunct rather than for "any error at all": the error must
	// carry a code isOptInFeatureUnavailableError tolerates, and must not be
	// discovery-sourced. Otherwise a change to the userinfo failure's code mapping
	// would leave this test green while no longer exercising the gate.
	if !isOptInFeatureUnavailableError(err) {
		t.Fatalf("test setup: expected a tolerated code so this test exercises the discovery-source conjunct, got %v: %v", status.Code(err), err)
	}
	if client.IsClmDiscoveryError(err) {
		t.Fatalf("test setup: expected a non-discovery-sourced error, got: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

func TestClmRoleBuilder_EntitlementsAndGrants_AreNoop(t *testing.T) {
	b := newClmRoleBuilder(nil)
	ctx := context.Background()

	roleResource, err := rs.NewRoleResource("FullSubscriber", clmRoleResourceType, "FullSubscriber", nil)
	if err != nil {
		t.Fatalf("NewRoleResource: %v", err)
	}

	if ents, res, err := b.Entitlements(ctx, roleResource, rs.SyncOpAttrs{}); err != nil || ents != nil || res != nil {
		t.Errorf("expected Entitlements to return (nil, nil, nil), got (%v, %v, %v)", ents, res, err)
	}
	if grants, res, err := b.Grants(ctx, roleResource, rs.SyncOpAttrs{}); err != nil || grants != nil || res != nil {
		t.Errorf("expected Grants to return (nil, nil, nil), got (%v, %v, %v)", grants, res, err)
	}
}
