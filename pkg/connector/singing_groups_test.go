package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

func TestSigningGroupBuilder_List_SkipsWithoutAnyClientCallWhenIncludeSigningGroupsUnset(t *testing.T) {
	// Regression test mirroring TestClmMemberBuilder_List_SkipsWithoutAnyClientCallWhenIncludeClmUnset:
	// includeSigningGroups must gate whether List() does any work, not just whether
	// signing_group is registered (it's always registered — see connector.go's
	// ResourceSyncers). A nil client proves this — any client call here would panic.
	b := newSigningGroupBuilder(nil, false)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when includeSigningGroups is unset, got %d", len(resources))
	}
	if res == nil || res.NextPageToken != "" {
		t.Errorf("expected an empty (non-paginating) result, got %+v", res)
	}
}
