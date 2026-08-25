package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// rewriteTransport rewrites all outgoing request URLs to the given target host.
type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return t.base.RoundTrip(req)
}

// multiAccountUserInfo simulates what DocuSign's /oauth/userinfo returns for an
// organisation with four accounts under the same tenant. The default account is
// the third (middle) one to verify that account selection finds the marked default
// rather than picking by position.
var multiAccountUserInfo = UserInfoResponse{
	Sub:   "service-account-user-id",
	Name:  "ConductorOne Service Account",
	Email: "c1-service@example.com",
	Accounts: []AccountInfo{
		{
			AccountId:   "aaaa-1111-alpha",
			AccountName: "Acme Alpha",
			BaseURI:     "", // patched to mock server URL in newTestClient
			IsDefault:   false,
		},
		{
			AccountId:   "bbbb-2222-beta",
			AccountName: "Acme Beta",
			BaseURI:     "", // patched to mock server URL in newTestClient
			IsDefault:   false,
		},
		{
			AccountId:   "cccc-3333-gamma",
			AccountName: "Acme Gamma",
			BaseURI:     "", // patched to mock server URL in newTestClient
			IsDefault:   true,
		},
		{
			AccountId:   "dddd-4444-delta",
			AccountName: "Acme Delta",
			BaseURI:     "", // patched to mock server URL in newTestClient
			IsDefault:   false,
		},
	},
}

// accountUsers holds the per-account user fixtures served by the mock API.
// Keys are DocuSign account IDs; values are the users returned for that account.
var accountUsers = map[string][]User{
	"aaaa-1111-alpha": {
		{UserId: "alpha-user-1", UserName: "Alice Alpha", Email: "alice@alpha.example.com"},
		{UserId: "alpha-user-2", UserName: "Bob Alpha", Email: "bob@alpha.example.com"},
	},
	"bbbb-2222-beta": {
		{UserId: "beta-user-1", UserName: "Carol Beta", Email: "carol@beta.example.com"},
	},
	"cccc-3333-gamma": {
		{UserId: "gamma-user-1", UserName: "Dave Gamma", Email: "dave@gamma.example.com"},
		{UserId: "gamma-user-2", UserName: "Eve Gamma", Email: "eve@gamma.example.com"},
		{UserId: "gamma-user-3", UserName: "Frank Gamma", Email: "frank@gamma.example.com"},
	},
}

// newTestClient creates a Client wired to a mock httptest.Server that acts as
// both the DocuSign OAuth userinfo endpoint and the API base URL.
// A rewriteTransport redirects all outgoing requests to the mock server
//
// The mock server handles:
//   - GET /oauth/userinfo                    → returns the provided userInfo
//   - GET /restapi/v2.1/accounts/{id}/users  → returns per-account fixtures from accountUsers
func newTestClient(t *testing.T, userInfo UserInfoResponse, configAccountId string) (*Client, *httptest.Server) {
	t.Helper()
	mockServer := httptest.NewServer(nil)

	// Deep-copy Accounts so we don't mutate the shared backing array of the
	// package-level fixture var (which would be a data race under t.Parallel()).
	accounts := make([]AccountInfo, len(userInfo.Accounts))
	copy(accounts, userInfo.Accounts)
	userInfo.Accounts = accounts
	for i := range userInfo.Accounts {
		userInfo.Accounts[i].BaseURI = mockServer.URL
	}

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/oauth/userinfo" {
			_ = json.NewEncoder(w).Encode(userInfo)
			return
		}

		// Match /restapi/v2.1/accounts/{accountId}/users
		var accountId string
		if _, err := fmt.Sscanf(r.URL.Path, "/restapi/v2.1/accounts/%s", &accountId); err == nil {
			// trim trailing path segments (e.g. "/users")
			accountId = strings.SplitN(accountId, "/", 2)[0]
			users := accountUsers[accountId]
			_ = json.NewEncoder(w).Encode(UsersResponse{Users: users})
			return
		}

		http.NotFound(w, r)
	})

	mockServerURL, _ := url.Parse(mockServer.URL)
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	testHTTPWrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})
	testTokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})

	c := NewClient(context.Background(), false, testTokenSource, configAccountId, "", testHTTPWrapper)

	return c, mockServer
}

// Three accounts under one tenant, default is the third — verifies that account
// selection finds the marked default rather than always picking the first entry.
func TestMultiAccountScenario(t *testing.T) {
	ctx := context.Background()

	t.Run("no account-id configured: syncs default account (Gamma, the third)", func(t *testing.T) {
		c, mockServer := newTestClient(t, multiAccountUserInfo, "")
		defer mockServer.Close()

		if err := c.ensureInitialized(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.accountId != "cccc-3333-gamma" {
			t.Errorf("expected default account Gamma, got accountId=%q", c.accountId)
		}
	})

	t.Run("account-id=Alpha: syncs first non-default account", func(t *testing.T) {
		c, mockServer := newTestClient(t, multiAccountUserInfo, "aaaa-1111-alpha")
		defer mockServer.Close()

		if err := c.ensureInitialized(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.accountId != "aaaa-1111-alpha" {
			t.Errorf("expected Alpha account, got accountId=%q", c.accountId)
		}
	})

	t.Run("account-id=Beta: syncs second non-default account", func(t *testing.T) {
		c, mockServer := newTestClient(t, multiAccountUserInfo, "bbbb-2222-beta")
		defer mockServer.Close()

		if err := c.ensureInitialized(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.accountId != "bbbb-2222-beta" {
			t.Errorf("expected Beta account, got accountId=%q", c.accountId)
		}
	})

	t.Run("account-id=Gamma: syncs default account when explicitly specified", func(t *testing.T) {
		c, mockServer := newTestClient(t, multiAccountUserInfo, "cccc-3333-gamma")
		defer mockServer.Close()

		if err := c.ensureInitialized(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.accountId != "cccc-3333-gamma" {
			t.Errorf("expected Gamma account, got accountId=%q", c.accountId)
		}
	})

	t.Run("wrong account-id: returns error", func(t *testing.T) {
		c, mockServer := newTestClient(t, multiAccountUserInfo, "zzzz-wrong-id")
		defer mockServer.Close()

		err := c.ensureInitialized(ctx)
		if err == nil {
			t.Fatal("expected error for unknown account ID, got nil")
		}
		if !strings.Contains(err.Error(), "zzzz-wrong-id") {
			t.Errorf("error should mention the bad account ID, got: %v", err)
		}
	})

	t.Run("two instances with same credentials sync different accounts independently", func(t *testing.T) {
		// Both connectors share the same OAuth credentials (same client_id/secret)
		// but target different accounts via --account-id.
		connectorAlpha, mockServerAlpha := newTestClient(t, multiAccountUserInfo, "aaaa-1111-alpha")
		defer mockServerAlpha.Close()
		connectorBeta, mockServerBeta := newTestClient(t, multiAccountUserInfo, "bbbb-2222-beta")
		defer mockServerBeta.Close()

		if err := connectorAlpha.ensureInitialized(ctx); err != nil {
			t.Fatalf("connectorAlpha error: %v", err)
		}
		if err := connectorBeta.ensureInitialized(ctx); err != nil {
			t.Fatalf("connectorBeta error: %v", err)
		}

		if connectorAlpha.accountId == connectorBeta.accountId {
			t.Errorf("both connectors resolved to the same account %q — they should be different", connectorAlpha.accountId)
		}
		if connectorAlpha.accountId != "aaaa-1111-alpha" {
			t.Errorf("connectorAlpha should sync Alpha, got %q", connectorAlpha.accountId)
		}
		if connectorBeta.accountId != "bbbb-2222-beta" {
			t.Errorf("connectorBeta should sync Beta, got %q", connectorBeta.accountId)
		}
	})
}

// TestMultiAccountResourceIsolation verifies that two connector instances targeting
// different accounts under the same tenant actually fetch different resources.
// This simulates the real-world scenario where a customer has multiple DocuSign
// accounts (e.g. one per business unit) and runs a separate connector for each.
func TestMultiAccountResourceIsolation(t *testing.T) {
	ctx := context.Background()

	connectorAlpha, mockServerAlpha := newTestClient(t, multiAccountUserInfo, "aaaa-1111-alpha")
	defer mockServerAlpha.Close()
	connectorBeta, mockServerBeta := newTestClient(t, multiAccountUserInfo, "bbbb-2222-beta")
	defer mockServerBeta.Close()
	connectorGamma, mockServerGamma := newTestClient(t, multiAccountUserInfo, "cccc-3333-gamma")
	defer mockServerGamma.Close()

	alphaUsers, _, _, err := connectorAlpha.GetUsers(ctx, PageOptions{})
	if err != nil {
		t.Fatalf("connectorAlpha.GetUsers error: %v", err)
	}
	betaUsers, _, _, err := connectorBeta.GetUsers(ctx, PageOptions{})
	if err != nil {
		t.Fatalf("connectorBeta.GetUsers error: %v", err)
	}
	gammaUsers, _, _, err := connectorGamma.GetUsers(ctx, PageOptions{})
	if err != nil {
		t.Fatalf("connectorGamma.GetUsers error: %v", err)
	}

	// Each account has a different number of users in the fixtures.
	if len(alphaUsers) != len(accountUsers["aaaa-1111-alpha"]) {
		t.Errorf("Alpha: expected %d users, got %d", len(accountUsers["aaaa-1111-alpha"]), len(alphaUsers))
	}
	if len(betaUsers) != len(accountUsers["bbbb-2222-beta"]) {
		t.Errorf("Beta: expected %d users, got %d", len(accountUsers["bbbb-2222-beta"]), len(betaUsers))
	}
	if len(gammaUsers) != len(accountUsers["cccc-3333-gamma"]) {
		t.Errorf("Gamma: expected %d users, got %d", len(accountUsers["cccc-3333-gamma"]), len(gammaUsers))
	}

	// Verify user IDs are account-specific — no cross-contamination.
	alphaIDs := make(map[string]bool)
	for _, u := range alphaUsers {
		alphaIDs[u.UserId] = true
	}
	for _, u := range betaUsers {
		if alphaIDs[u.UserId] {
			t.Errorf("user %q appears in both Alpha and Beta — accounts are leaking into each other", u.UserId)
		}
	}
}

// newCountingPermissionProfilesClient wires a Client to a mock server that counts real
// GET /permission_profiles hits, to distinguish a real network call from one served by
// uhttp's shared GET cache.
func newCountingPermissionProfilesClient(t *testing.T) (*Client, *int) {
	t.Helper()
	calls := 0

	mockServer := httptest.NewServer(nil)
	t.Cleanup(mockServer.Close)
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/userinfo":
			_ = json.NewEncoder(w).Encode(UserInfoResponse{
				Sub: "service-account-user-id",
				Accounts: []AccountInfo{
					{AccountId: "acct-1", AccountName: "Acme", BaseURI: mockServer.URL, IsDefault: true},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/permission_profiles"):
			calls++
			_ = json.NewEncoder(w).Encode(PermissionProfilesResponse{
				PermissionProfiles: []PermissionProfile{{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"}},
			})
		default:
			http.NotFound(w, r)
		}
	})

	mockServerURL, _ := url.Parse(mockServer.URL)
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	return NewClient(context.Background(), false, tokenSource, "", "", wrapper), &calls
}

// TestGetPermissionProfiles_CachingSplitByCaller is a regression test for a review
// finding: GetPermissionProfiles used to unconditionally bypass uhttp's GET cache for
// every caller, but List and Revoke (unlike userBuilder) don't memoize this call across
// syncs — sharing the cache between them when both fire in the same sync saves a real
// network call, and unconditional WithNoCache() silently turned that 1 call into 2.
// GetPermissionProfiles must still be cacheable; only GetPermissionProfilesFresh (the
// dedicated variant for userBuilder's cross-sync-safe memoization) bypasses the cache.
func TestGetPermissionProfiles_CachingSplitByCaller(t *testing.T) {
	ctx := context.Background()

	t.Run("GetPermissionProfiles is cacheable: two calls, one real request", func(t *testing.T) {
		c, calls := newCountingPermissionProfilesClient(t)
		for i := 0; i < 2; i++ {
			if _, _, err := c.GetPermissionProfiles(ctx); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if *calls != 1 {
			t.Errorf("expected 1 real request across 2 GetPermissionProfiles calls (cache should serve the second), got %d", *calls)
		}
	})

	t.Run("GetPermissionProfilesFresh always issues a real request", func(t *testing.T) {
		c, calls := newCountingPermissionProfilesClient(t)
		for i := 0; i < 2; i++ {
			if _, _, err := c.GetPermissionProfilesFresh(ctx); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if *calls != 2 {
			t.Errorf("expected 2 real requests across 2 GetPermissionProfilesFresh calls (no caching), got %d", *calls)
		}
	})
}
