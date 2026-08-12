package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
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

// TestClmRoleBuilder_List_FailsWhenClmUnavailable is a deliberate design choice, not an
// oversight: clm_role is OptInRequired (resource_types.go), so List() only ever runs
// once a customer has explicitly enabled it in their sync config, and C1's opt-in toggle
// has no upstream check against DocuSign — a customer can enable it without actually
// having a CLM subscription. When EnsureClmReady then fails, that's a real
// misconfiguration (wrong resource enabled, or the feature needs activating), not an
// expected/transient state, so it must fail the sync loudly rather than silently
// succeed with zero roles indefinitely.
func TestClmRoleBuilder_List_FailsWhenClmUnavailable(t *testing.T) {
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmRoleBuilder(badClient)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected List to fail when CLM is unavailable, got nil error")
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
