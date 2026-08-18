package connector

import (
	"context"
	"net/http"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	"golang.org/x/oauth2"
)

// alwaysRegisteredTypeIDs are the resource types ResourceSyncers registers on every
// sync, with no config flag gating them. The CLM types belong here deliberately:
// registering conditionally would make ListResourceTypes() advertise fewer types than a
// prior sync did, and C1 can then bucket every previously-synced resource and grant of a
// vanished type as deleted. Gating happens via &v2.OptInRequired{} (resource_types.go)
// alone, not by omitting the builder — Connector.Validate() fails the sync loudly, up
// front, rather than tolerating an unavailable-feature error, when a customer opts in
// without a reachable CLM subscription (see connector.go's Validate doc comment).
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

// TestConnectorValidate covers both readiness checks Validate() centralizes (see
// connector.go's doc comment): EnsureReady (base eSignature credentials) runs
// unconditionally, EnsureClmReady only when includeClm is set. The includeClm=false
// subtests are the ones that matter most: one proves the CLM-specific check is
// genuinely skipped rather than coincidentally passing, the other proves Validate() now
// catches a misconfigured account upfront even when CLM was never opted into, instead
// of leaving that to whichever builder's List() happens to run first.
func TestConnectorValidate(t *testing.T) {
	s, c := clmtest.NewServer(t)
	badClmClient := s.NewClientWithToken("wrong-token")
	ctx := context.Background()

	t.Run("includeClm=true, base and CLM both reachable: succeeds", func(t *testing.T) {
		d := &Connector{client: c, includeClm: true}
		if _, err := d.Validate(ctx); err != nil {
			t.Errorf("expected Validate to succeed, got %v", err)
		}
	})

	t.Run("includeClm=true, CLM unreachable: fails loudly", func(t *testing.T) {
		d := &Connector{client: badClmClient, includeClm: true}
		if _, err := d.Validate(ctx); err == nil {
			t.Error("expected Validate to fail when CLM is unreachable, got nil error")
		}
	})

	t.Run("includeClm=false, CLM unreachable but base fine: still succeeds", func(t *testing.T) {
		// badClmClient's bad token only fails clmtest's requireAuth-gated CLM routes —
		// its /oauth/userinfo (the base check) succeeds regardless of token (see
		// clmtest/server.go's handleUserInfo) — so a nil error here proves the
		// CLM-specific check was genuinely skipped, not just coincidentally passing.
		d := &Connector{client: badClmClient, includeClm: false}
		if _, err := d.Validate(ctx); err != nil {
			t.Errorf("expected Validate to skip the CLM check (nil error) when includeClm is false, got %v", err)
		}
	})

	t.Run("includeClm=false, base credentials bad: fails", func(t *testing.T) {
		badBaseClient := newSigningGroupsTestClient(t, http.StatusUnauthorized)
		d := &Connector{client: badBaseClient, includeClm: false}
		if _, err := d.Validate(ctx); err == nil {
			t.Error("expected Validate to fail on bad base credentials even when includeClm is false, got nil error")
		}
	})
}

// TestNewWithRefreshToken_StoresIncludeClm is a regression test for a real bug caught
// during review: NewWithRefreshToken already received includeClm as a parameter (used
// for OAuth scope selection via client.New) but silently dropped it instead of storing
// it on the returned Connector, so Validate() would never have run its CLM check for
// any connector built this way. A non-empty baseURLOverride makes client.New build a
// StaticTokenSource with zero network I/O (see client.go), so this needs no mock server.
func TestNewWithRefreshToken_StoresIncludeClm(t *testing.T) {
	ctx := context.Background()

	for _, includeClm := range []bool{true, false} {
		cb, err := NewWithRefreshToken(
			ctx, false, "client-id", "client-secret", "https://redirect.example.com",
			"refresh-token", "account-1", false, includeClm,
			"https://clm.example.com", "https://api.example.com", false,
		)
		if err != nil {
			t.Fatalf("includeClm=%v: NewWithRefreshToken: %v", includeClm, err)
		}
		if cb.includeClm != includeClm {
			t.Errorf("includeClm=%v: expected Connector.includeClm=%v, got %v", includeClm, includeClm, cb.includeClm)
		}
	}
}

// TestNewWithClient_StoresIncludeClm is a regression test matching its two siblings
// above, for consistency — see NewWithClient's doc comment for why this constructor is
// kept despite having no caller anywhere in this repo today. nil is a valid client here
// since NewWithClient only stores it, never calls it (same pattern as newClmRoleBuilder(nil)
// elsewhere in this package).
func TestNewWithClient_StoresIncludeClm(t *testing.T) {
	for _, includeClm := range []bool{true, false} {
		cb, err := NewWithClient(nil, false, includeClm, false)
		if err != nil {
			t.Fatalf("includeClm=%v: NewWithClient: %v", includeClm, err)
		}
		if cb.includeClm != includeClm {
			t.Errorf("includeClm=%v: expected Connector.includeClm=%v, got %v", includeClm, includeClm, cb.includeClm)
		}
	}
}

// TestNewWithTokenSource_StoresIncludeClm is a regression test for the more serious of
// the two constructor gaps: NewWithTokenSource — the ConductorOne-hosted path, i.e. the
// common production case — had no includeClm parameter at all, so Validate() would have
// silently never checked CLM readiness for the majority deployment path. client.NewClient
// makes zero network I/O at construction (see client.go), so this needs no mock server.
func TestNewWithTokenSource_StoresIncludeClm(t *testing.T) {
	ctx := context.Background()
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"})

	for _, includeClm := range []bool{true, false} {
		cb, err := NewWithTokenSource(
			ctx, false, tokenSource, "account-1", false, includeClm, "https://clm.example.com", false,
		)
		if err != nil {
			t.Fatalf("includeClm=%v: NewWithTokenSource: %v", includeClm, err)
		}
		if cb.includeClm != includeClm {
			t.Errorf("includeClm=%v: expected Connector.includeClm=%v, got %v", includeClm, includeClm, cb.includeClm)
		}
	}
}
