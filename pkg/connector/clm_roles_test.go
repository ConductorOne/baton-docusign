package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

func TestClmRoleBuilder_List(t *testing.T) {
	// Not backed by an API call — the mock server isn't even needed here, unlike every
	// other CLM builder's List test.
	b := newClmRoleBuilder(nil, true)
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

func TestClmRoleBuilder_List_SkipsWhenIncludeClmUnset(t *testing.T) {
	// Regression test for the "unconditional registration exposes every account to the
	// CLM base-URL discovery call" finding: includeClm must gate whether List() does
	// any work, not just whether the builder is registered. clm_role makes no client
	// call at all, so this only confirms it doesn't emit the 5 static resources when
	// unset — the other 4 CLM builders' equivalent tests confirm the client is never
	// even reached.
	b := newClmRoleBuilder(nil, false)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when includeClm is unset, got %d", len(resources))
	}
	if res == nil || res.NextPageToken != "" {
		t.Errorf("expected an empty (non-paginating) result, got %+v", res)
	}
}

func TestClmRoleBuilder_EntitlementsAndGrants_AreNoop(t *testing.T) {
	b := newClmRoleBuilder(nil, true)
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
