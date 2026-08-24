package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

func TestClmRoleBuilder_List(t *testing.T) {
	// The role set isn't backed by an API call at all — CLM availability is checked
	// once, up front, by Connector.Validate() (see connector_test.go), not here — so
	// this needs no client at all.
	b := newClmRoleBuilder()
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

func TestClmRoleBuilder_EntitlementsAndGrants_AreNoop(t *testing.T) {
	b := newClmRoleBuilder()
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
