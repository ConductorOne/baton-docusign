package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
)

// TestResourceSyncers_AlwaysRegistersEveryBuilder is a regression test for the
// wipe-risk fix: ResourceSyncers() must register every resource type unconditionally
// — including the opt-in ones (signing_group and the 5 CLM types) — regardless of
// includeClm, so that toggling a config flag never makes a resource type disappear
// from ListResourceTypes() and get treated as fully deleted by C1. Gating now happens
// via &v2.OptInRequired{} (resource_types.go) and each opt-in builder's List()
// tolerating an unavailable-feature error (helper.go), not by omitting the builder.
func TestResourceSyncers_AlwaysRegistersEveryBuilder(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	wantTypeIDs := map[string]bool{
		"user":               true,
		"group":              true,
		"permission_profile": true,
		"signing_group":      true,
		"clm_member":         true,
		"clm_role":           true,
		"clm_group":          true,
		"clm_permission_set": true,
		"clm_folder":         true,
	}

	for _, includeClm := range []bool{false, true} {
		d := &Connector{client: c, includeClm: includeClm}
		syncers := d.ResourceSyncers(ctx)

		if len(syncers) != len(wantTypeIDs) {
			t.Fatalf("includeClm=%v: expected %d registered syncers, got %d", includeClm, len(wantTypeIDs), len(syncers))
		}

		got := make(map[string]bool, len(syncers))
		for _, s := range syncers {
			got[s.ResourceType(ctx).Id] = true
		}
		for id := range wantTypeIDs {
			if !got[id] {
				t.Errorf("includeClm=%v: expected resource type %q to be registered, but it wasn't", includeClm, id)
			}
		}
	}
}
