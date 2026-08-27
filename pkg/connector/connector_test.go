package connector

import (
	"context"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	cfg "github.com/conductorone/baton-docusign/pkg/config"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
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
	"clm_workflow_queue",
}

// TestClmMemberResourceType_HasWorkflowQueueChildResourceType pins the annotation that
// drives clm_workflow_queue's whole redesign: clm_workflow_queue is modeled as
// clmMemberResourceType's ChildResourceType (see resource_types.go's doc and
// clm_workflow_queues.go) rather than syncing independently, so the SDK's child-resource
// scheduling can call clmWorkflowQueueBuilder.List() once per synced clm_member. A
// missing or misconfigured annotation here would silently stop that scheduling from ever
// firing, with no compile-time signal.
func TestClmMemberResourceType_HasWorkflowQueueChildResourceType(t *testing.T) {
	annos := annotations.Annotations(clmMemberResourceType.Annotations)
	var child v2.ChildResourceType
	ok, err := annos.Pick(&child)
	if err != nil {
		t.Fatalf("Pick(ChildResourceType): %v", err)
	}
	if !ok {
		t.Fatal("expected clmMemberResourceType to carry a ChildResourceType annotation")
	}
	if child.ResourceTypeId != clmWorkflowQueueResourceType.Id {
		t.Errorf("expected ChildResourceType.ResourceTypeId %q, got %q", clmWorkflowQueueResourceType.Id, child.ResourceTypeId)
	}
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
		badBaseClient := newSigningGroupsTestClient(t, http.StatusUnauthorized, http.StatusOK)
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
			"https://clm.example.com", "https://api.example.com", false, false,
		)
		if err != nil {
			t.Fatalf("includeClm=%v: NewWithRefreshToken: %v", includeClm, err)
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
			ctx, false, tokenSource, "account-1", false, includeClm, "https://clm.example.com", false, false,
		)
		if err != nil {
			t.Fatalf("includeClm=%v: NewWithTokenSource: %v", includeClm, err)
		}
		if cb.includeClm != includeClm {
			t.Errorf("includeClm=%v: expected Connector.includeClm=%v, got %v", includeClm, includeClm, cb.includeClm)
		}
	}
}

// nonClmAllowlist derives the "no CLM types opted in" test case from
// alwaysRegisteredTypeIDs — the connector's own source of truth for always-registered
// resource types — instead of a second hardcoded literal, so there's only one list to
// keep in sync with reality (this doesn't by itself catch ci.yaml drifting from this
// value; TestNonClmAllowlistMatchesCI below asserts that separately). signing_group is
// added separately since it's intentionally NOT in alwaysRegisteredTypeIDs
// (conditionally registered, see that var's doc) but is present in CI's real
// BATON_SYNC_RESOURCE_TYPES allowlist (ci.yaml).
func nonClmAllowlist() []string {
	ids := []string{"signing_group"}
	for _, id := range alwaysRegisteredTypeIDs {
		if !strings.HasPrefix(id, "clm_") {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestNonClmAllowlistMatchesCI is the actual enforcement ci.yaml's own comment asks
// for: a new non-CLM resource type in alwaysRegisteredTypeIDs (and so in
// nonClmAllowlist()) is worthless as a drift guard unless something also checks
// ci.yaml's BATON_SYNC_RESOURCE_TYPES against it. Parses the real workflow file rather
// than duplicating its value a third time.
func TestNonClmAllowlistMatchesCI(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yaml")
	if err != nil {
		t.Fatalf("reading ci.yaml: %v", err)
	}
	// map[string]any, not map[string]string: an unrelated non-string workflow-level env
	// var (e.g. BATON_FOO: true) would otherwise fail the whole decode with a
	// yaml.TypeError, failing this test somewhere unrelated to the one key it actually
	// guards.
	var workflow struct {
		Env map[string]any `yaml:"env"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parsing ci.yaml: %v", err)
	}
	value, ok := workflow.Env["BATON_SYNC_RESOURCE_TYPES"]
	if !ok {
		t.Fatal("ci.yaml's workflow-level env has no BATON_SYNC_RESOURCE_TYPES — did it move to a per-job env block?")
	}
	raw, ok := value.(string)
	if !ok {
		t.Fatalf("ci.yaml's BATON_SYNC_RESOURCE_TYPES decoded as %T, expected a string", value)
	}

	got := strings.Split(raw, ",")
	want := nonClmAllowlist()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ci.yaml's BATON_SYNC_RESOURCE_TYPES (%v) doesn't match nonClmAllowlist() (%v) — "+
			"update ci.yaml's allowlist (or alwaysRegisteredTypeIDs) to match", got, want)
	}
}

// TestNew_IncludeClmDerivation pins New()'s opts.WillSyncResourceType(clm*) disjunction
// (connector.go) directly — CI's own BATON_SYNC_RESOURCE_TYPES allowlist depends on this
// exact logic to keep includeClm=false, and none of the other tests here exercise it: a
// renamed clm_* resource type ID or a term dropped from the disjunction would silently
// stop being caught by anything else in this file. Routes through opts.TokenSource
// (client.NewClient makes zero network I/O at construction, see client.go), so this
// needs no mock server and no refresh token.
func TestNew_IncludeClmDerivation(t *testing.T) {
	ctx := context.Background()
	docusignCfg := &cfg.Docusign{DocusignClientId: "client-id", DocusignClientSecret: "client-secret"}
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"})

	tests := []struct {
		name                      string
		syncResourceTypeIDs       []string
		wantIncludeClm            bool
		wantIncludeWorkflowQueues bool
	}{
		{"no filter (opts.SyncResourceTypeIDs empty): syncs everything, including CLM", nil, true, true},
		{"CI's actual allowlist: no clm_* type present", nonClmAllowlist(), false, false},
		{"clm_member present", []string{"user", clmMemberResourceType.Id}, true, false},
		{"clm_role present", []string{"user", clmRoleResourceType.Id}, true, false},
		{"clm_group present", []string{"user", clmGroupResourceType.Id}, true, false},
		{"clm_permission_set present", []string{"user", clmPermissionSetResourceType.Id}, true, false},
		{"clm_folder present", []string{"user", clmFolderResourceType.Id}, true, false},
		{"clm_workflow_queue present", []string{"user", clmWorkflowQueueResourceType.Id}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &cli.ConnectorOpts{TokenSource: tokenSource, SyncResourceTypeIDs: tt.syncResourceTypeIDs}
			built, _, err := New(ctx, docusignCfg, opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			cb, ok := built.(*Connector)
			if !ok {
				t.Fatalf("New returned %T, expected *Connector", built)
			}
			if cb.includeClm != tt.wantIncludeClm {
				t.Errorf("SyncResourceTypeIDs=%v: expected includeClm=%v, got %v", tt.syncResourceTypeIDs, tt.wantIncludeClm, cb.includeClm)
			}
			if cb.includeWorkflowQueues != tt.wantIncludeWorkflowQueues {
				t.Errorf("SyncResourceTypeIDs=%v: expected includeWorkflowQueues=%v, got %v", tt.syncResourceTypeIDs, tt.wantIncludeWorkflowQueues, cb.includeWorkflowQueues)
			}
		})
	}
}
