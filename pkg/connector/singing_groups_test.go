package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// rewriteTransport is already declared in users_test.go (same package) — reused here
// rather than duplicated.

// testSigningGroupID and testSigningGroupName are the one signing group
// newSigningGroupsTestClient's mock seeds in its /signing_groups response.
const (
	testSigningGroupID   = "sg-1"
	testSigningGroupName = "Test Signing Group"
)

// newSigningGroupsTestClient builds a *client.Client wired to a mock server serving
// /oauth/userinfo plus a minimal /signing_groups response (one seeded group).
// signingGroupsStatus forces the /signing_groups response itself to fail — the
// realistic "feature not enabled on this account" signal — independent of
// userInfoStatus, which only fails base account discovery. Pass http.StatusOK for
// either to use the normal (working) response.
func newSigningGroupsTestClient(t *testing.T, userInfoStatus, signingGroupsStatus int) *client.Client {
	t.Helper()
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/userinfo" {
			if userInfoStatus != http.StatusOK {
				w.WriteHeader(userInfoStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.UserInfoResponse{
				Sub:  "test-user",
				Name: "Test User",
				Accounts: []client.AccountInfo{
					{AccountId: "acct-1", AccountName: "Test Account", IsDefault: true, BaseURI: mockServer.URL},
				},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/signing_groups") {
			if signingGroupsStatus != http.StatusOK {
				w.WriteHeader(signingGroupsStatus)
				_ = json.NewEncoder(w).Encode(client.ErrorResponse{ErrorCode: "FEATURE_NOT_ENABLED", ErrorMessage: "Signing groups are not enabled for this account"})
				return
			}
			// One seeded group is enough to exercise both the happy path and
			// parseIntoSigningGroupResource, without a full pagination fixture (no next
			// page: the zero-valued embedded Page makes getNextToken's
			// EndPosition+1 < TotalSetSize read 0+1 < 0, false).
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.SigningGroupResponse{
				SigningGroups: []client.SigningGroup{
					{SigningGroupId: testSigningGroupID, GroupName: testSigningGroupName},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(mockServer.Close)

	mockServerURL, err := url.Parse(mockServer.URL)
	if err != nil {
		t.Fatalf("parsing mock server URL: %v", err)
	}
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})

	return client.NewClient(context.Background(), false, tokenSource, "", "", wrapper)
}

// TestSigningGroupBuilder_List_FailsWhenUnavailable is a regression test: signing_group
// is gated behind the --include-signing-groups flag (connector.go), but that flag
// doesn't validate the account actually has the feature before letting an operator turn
// it on. List() must propagate any error (here, a 401 from eSignature account discovery)
// instead of tolerating it and silently syncing zero signing groups.
func TestSigningGroupBuilder_List_FailsWhenUnavailable(t *testing.T) {
	c := newSigningGroupsTestClient(t, http.StatusUnauthorized, http.StatusOK)
	b := newSigningGroupBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err == nil {
		t.Fatal("expected List to fail when signing groups are unavailable, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

// TestSigningGroupBuilder_List_FailsOnSigningGroupsEndpointError is a regression test
// for the tolerance the fail-loud commit actually removed: the old code caught a
// 401/403/404 from the /signing_groups endpoint itself (this account's real "feature
// not enabled" signal), not from base account discovery — the sibling test above forces
// a 401 on /oauth/userinfo instead, which Connector.Validate()'s EnsureReady already
// catches for every resource type and never reached the removed branch.
func TestSigningGroupBuilder_List_FailsOnSigningGroupsEndpointError(t *testing.T) {
	c := newSigningGroupsTestClient(t, http.StatusOK, http.StatusForbidden)
	b := newSigningGroupBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err == nil {
		t.Fatal("expected List to fail when the /signing_groups endpoint itself errors, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

// TestSigningGroupBuilder_List_Succeeds is a sanity check for
// newSigningGroupsTestClient itself: confirms the happy path (account discovery
// succeeds) reaches List()'s normal return and correctly parses the one seeded signing
// group via parseIntoSigningGroupResource, distinguishing a correctly-wired mock from
// the fail-loud test above passing only because everything errors regardless.
func TestSigningGroupBuilder_List_Succeeds(t *testing.T) {
	c := newSigningGroupsTestClient(t, http.StatusOK, http.StatusOK)
	b := newSigningGroupBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil SyncOpResults")
	}
	if len(resources) != 1 {
		t.Fatalf("expected the one seeded signing group, got %d: %+v", len(resources), resources)
	}
	if got := resources[0].Id.Resource; got != testSigningGroupID {
		t.Errorf("expected resource ID %q, got %q", testSigningGroupID, got)
	}
	if got := resources[0].DisplayName; got != testSigningGroupName {
		t.Errorf("expected display name %q, got %q", testSigningGroupName, got)
	}
}
