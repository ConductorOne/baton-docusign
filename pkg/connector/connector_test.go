package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
)

// alwaysRegisteredTypeIDs are the resource types ResourceSyncers registers on every
// sync, with no config flag gating them. The CLM types belong here deliberately:
// registering conditionally would make ListResourceTypes() advertise fewer types than a
// prior sync did, and C1 can then bucket every previously-synced resource and grant of a
// vanished type as deleted. Gating happens via &v2.OptInRequired{} (resource_types.go)
// alone, not by omitting the builder — List() now fails loudly rather than tolerating an
// unavailable-feature error when a customer opts in without a reachable CLM subscription
// (see clm_roles.go's doc comment).
var alwaysRegisteredTypeIDs = []string{
	"user",
	"group",
	"permission_profile",
	"clm_member",
	"clm_role",
	"clm_group",
	"clm_permission_set",
	"clm_folder",
}

func registeredTypeIDs(ctx context.Context, d *Connector) map[string]bool {
	syncers := d.ResourceSyncers(ctx)
	got := make(map[string]bool, len(syncers))
	for _, s := range syncers {
		got[s.ResourceType(ctx).Id] = true
	}
	return got
}

func TestResourceSyncers_AlwaysRegistersCoreAndClmBuilders(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	for _, includeSigningGroups := range []bool{false, true} {
		d := &Connector{client: c, includeSigningGroups: includeSigningGroups}
		got := registeredTypeIDs(ctx, d)

		for _, id := range alwaysRegisteredTypeIDs {
			if !got[id] {
				t.Errorf("includeSigningGroups=%v: expected resource type %q to always be registered, but it wasn't",
					includeSigningGroups, id)
			}
		}
	}
}

// TestResourceSyncers_SigningGroupRegistrationFollowsFlag pins the CURRENT behaviour,
// not the desired one: unlike the CLM types, signing_group is registered only when
// includeSigningGroups is set, so ListResourceTypes() varies between syncs and carries
// the same delete-bucketing risk the CLM types were changed to avoid. If signing_group
// registration is made unconditional (moving the gate to an &v2.OptInRequired{}
// annotation, as the CLM types do), fold "signing_group" into alwaysRegisteredTypeIDs
// and delete this test.
func TestResourceSyncers_SigningGroupRegistrationFollowsFlag(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	for _, includeSigningGroups := range []bool{false, true} {
		d := &Connector{client: c, includeSigningGroups: includeSigningGroups}
		got := registeredTypeIDs(ctx, d)

		if got["signing_group"] != includeSigningGroups {
			t.Errorf("includeSigningGroups=%v: expected signing_group registered=%v, got %v",
				includeSigningGroups, includeSigningGroups, got["signing_group"])
		}

		wantLen := len(alwaysRegisteredTypeIDs)
		if includeSigningGroups {
			wantLen++
		}
		if len(got) != wantLen {
			t.Errorf("includeSigningGroups=%v: expected %d registered syncers, got %d (%v)",
				includeSigningGroups, wantLen, len(got), got)
		}
	}
}
