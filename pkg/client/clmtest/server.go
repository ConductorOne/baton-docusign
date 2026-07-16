// Package clmtest provides an in-memory mock of the DocuSign eSignature
// /oauth/userinfo endpoint, CLM's account discovery endpoint, and the DocuSign CLM
// Object API, for use by tests in pkg/client and pkg/connector. It exists because no
// CLM production/sandbox tenant was available to validate this integration directly —
// this is how the CLM sync/provisioning code paths get exercised instead.
//
// # Anti-drift note
//
// This mock replicates the endpoint paths, query params, and response/request shapes
// documented for the real CLM API — not whatever the connector happens to call. If
// the connector and this mock ever disagree, treat that as a bug in the connector to
// fix, not something to paper over here.
//
// # Auth
//
// NewServer (used by this package's own Go tests) mocks no real OAuth flow: it returns
// a *client.Client already wired with a static token, and every endpoint handler
// requires "Authorization: Bearer <token>" matching that exact token — mirroring the
// strictness of the real API without needing a token endpoint.
//
// RunStandalone (see cmd/test-server) is for manually pointing a real baton-docusign
// binary at this mock via --clm-base-url, while the connector still authenticates for
// real against DocuSign. Its bearer check only requires *a* token, not an exact value,
// since this mock has no way to independently validate a real DocuSign-issued token.
//
// # Endpoints
//
//	GET   /oauth/userinfo                              — eSignature account discovery (ensureInitialized)
//	GET   /api/v2/{accountId}/account                   — CLM account discovery (ensureClmInitialized)
//	POST  /v2/{accountId}/folders/search                — SearchFolders
//	GET   /v2/{accountId}/folders/{id}                  — GetFolder (supports ?expand=Security)
//	PATCH /v2/{accountId}/folders/{id}                  — PatchFolderSecurity
//	GET   /v2/{accountId}/groups                        — ListGroups
//	GET   /v2/{accountId}/groups/{id}/groupmembers       — GetGroupMembers
//	GET   /v2/{accountId}/members                       — ListMembers
//	GET   /v2/{accountId}/members/{id}/groups            — GetMemberGroups (paginated)
//	PATCH /v2/{accountId}/members/{id}                  — PatchMemberGroups (additive)
//	PUT   /v2/{accountId}/members/{id}                  — PutMemberGroups (full-replace)
//	GET   /v2/{accountId}/permissionsets                 — ListPermissionSets
package clmtest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// AccountID is the fixed eSignature/CLM account ID used by every seeded fixture.
const AccountID = "acct-clm-test"

const testBearerToken = "test-clm-token" //nolint:gosec // fixture token for tests, not a credential

// rewriteTransport rewrites all outgoing request URLs to the given target host — needed
// because ensureClmReady always calls eSignature's ensureInitialized first, which hits
// a hardcoded /oauth/userinfo host that a token's Extra data can't override.
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

// Server is the mock CLM (+ eSignature account discovery) API.
type Server struct {
	t   testing.TB
	srv *httptest.Server

	// baseURL is the externally reachable base URL for this server: srv.URL for
	// NewServer's in-process httptest.Server, or the standalone listen address for
	// RunStandalone. Href builders and URL() use this instead of srv.URL directly so
	// both modes share the same logic.
	baseURL string

	// strictAuth mirrors the real API's auth strictness for NewServer's Go tests: it
	// rejects anything but the one fixed testBearerToken. RunStandalone sets this false
	// because the connector there authenticates via a real DocuSign OAuth flow — the
	// bearer token it presents is real and valid but unknown to this mock in advance, so
	// only requiring *a* token (not the exact value) is the best this mock can enforce.
	strictAuth bool

	mu sync.Mutex

	folders     map[string]*client.ClmFolder
	folderOrder []string

	groups       map[string]*client.ClmGroup
	groupOrder   []string
	groupMembers map[string][]string // groupID -> memberIDs, insertion order preserved

	members      map[string]*client.ClmMember
	memberOrder  []string
	memberGroups map[string][]string // memberID -> groupIDs, mutable via Patch/Put

	permissionSets     map[string]*client.ClmPermissionSet
	permissionSetOrder []string

	memberGroupsRequests int // count of GET .../members/{id}/groups calls, for pagination assertions
}

// MemberGroupsRequestCount returns how many times GET .../members/{id}/groups has been
// called, across all members — used to assert that GetMemberGroups actually issues
// multiple requests when a member has more groups than fit on one page, rather than
// silently returning a truncated single page.
func (s *Server) MemberGroupsRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memberGroupsRequests
}

// URL returns the mock server's base URL — also what handleClmAccountDiscovery
// returns as the CLM API base URL.
func (s *Server) URL() string { return s.baseURL }

// FolderHref, GroupHref, MemberHref build the Href a real CLM response would use for a
// given ID — tests use these to seed Security entries / assert on parsed IDs.
func (s *Server) FolderHref(id string) string {
	return fmt.Sprintf("%s/v2/%s/folders/%s", s.baseURL, AccountID, id)
}

func (s *Server) GroupHref(id string) string {
	return fmt.Sprintf("%s/v2/%s/groups/%s", s.baseURL, AccountID, id)
}

func (s *Server) MemberHref(id string) string {
	return fmt.Sprintf("%s/v2/%s/members/%s", s.baseURL, AccountID, id)
}

// MemberGroups returns the current (test-visible) group membership for a member, for
// assertions after a Grant/Revoke round trip.
func (s *Server) MemberGroups(memberID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.memberGroups[memberID]))
	copy(out, s.memberGroups[memberID])
	return out
}

// FolderSecurity returns the current (test-visible) Security entries for a folder, for
// assertions after a Grant/Revoke round trip.
func (s *Server) FolderSecurity(folderID string) []client.ClmSecurityEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.folders[folderID]
	if !ok {
		return nil
	}
	out := make([]client.ClmSecurityEntry, len(f.Security))
	copy(out, f.Security)
	return out
}

// newState allocates the maps/slices a fresh Server needs — shared by NewServer and
// RunStandalone so both construct exactly the same seeded state.
func newState() *Server {
	return &Server{
		folders:        make(map[string]*client.ClmFolder),
		groups:         make(map[string]*client.ClmGroup),
		groupMembers:   make(map[string][]string),
		members:        make(map[string]*client.ClmMember),
		memberGroups:   make(map[string][]string),
		permissionSets: make(map[string]*client.ClmPermissionSet),
	}
}

// newMux registers every endpoint handler on a fresh mux — shared by NewServer and
// RunStandalone so both serve exactly the same routes.
func newMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/userinfo", s.handleUserInfo)
	mux.HandleFunc("GET /api/v2/{accountId}/account", s.requireAuth(s.handleClmAccountDiscovery))
	mux.HandleFunc("POST /v2/{accountId}/folders/search", s.requireAuth(s.handleSearchFolders))
	mux.HandleFunc("GET /v2/{accountId}/folders/{id}", s.requireAuth(s.handleGetFolder))
	mux.HandleFunc("PATCH /v2/{accountId}/folders/{id}", s.requireAuth(s.handlePatchFolder))
	mux.HandleFunc("GET /v2/{accountId}/groups", s.requireAuth(s.handleListGroups))
	mux.HandleFunc("GET /v2/{accountId}/groups/{id}/groupmembers", s.requireAuth(s.handleGroupMembers))
	mux.HandleFunc("GET /v2/{accountId}/members", s.requireAuth(s.handleListMembers))
	mux.HandleFunc("GET /v2/{accountId}/members/{id}/groups", s.requireAuth(s.handleMemberGroups))
	mux.HandleFunc("PATCH /v2/{accountId}/members/{id}", s.requireAuth(s.handlePatchMember))
	mux.HandleFunc("PUT /v2/{accountId}/members/{id}", s.requireAuth(s.handlePutMember))
	mux.HandleFunc("GET /v2/{accountId}/permissionsets", s.requireAuth(s.handleListPermissionSets))
	return mux
}

// NewServer starts the mock server, seeds it (see seed.go), and returns both the raw
// Server (for direct state assertions) and a *client.Client wired to talk to it exactly
// as it would talk to the real DocuSign/CLM hosts.
func NewServer(t testing.TB) (*Server, *client.Client) {
	t.Helper()

	s := newState()
	s.t = t
	s.strictAuth = true

	mux := newMux(s)
	s.srv = httptest.NewServer(mux)
	s.baseURL = s.srv.URL
	t.Cleanup(s.srv.Close)

	seed(s)

	mockServerURL, _ := url.Parse(s.srv.URL)
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})

	tok := &oauth2.Token{AccessToken: testBearerToken}
	tokenSource := oauth2.StaticTokenSource(tok)

	c := client.NewClient(context.Background(), false, tokenSource, "", "", wrapper)

	return s, c
}

// RunStandalone starts the same CLM mock as NewServer, but as a real, long-lived HTTP
// server on addr (e.g. "localhost:8765") instead of an ephemeral in-process
// httptest.Server — for manually pointing a real baton-docusign binary at it via
// --clm-base-url to exercise the CLM sync/provisioning paths against an account that
// has no CLM subscription of its own. Blocks until the server errors or is shut down.
func RunStandalone(addr string) error {
	s := newState()
	s.strictAuth = false
	s.baseURL = "http://" + addr

	seed(s)

	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(s),
		ReadHeaderTimeout: 30 * time.Second,
	}

	log.Printf("clmtest: standalone CLM mock listening on %s (account id %q)", addr, AccountID)
	return srv.ListenAndServe()
}

// requireAuth enforces that a bearer token is present. In strict mode (NewServer's Go
// tests) it must equal the one fixed testBearerToken, mirroring the real API's
// strictness. In standalone mode it only checks that a token is present — see the
// Server.strictAuth field doc for why an exact match isn't possible there.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		unauthorized := false
		if s.strictAuth {
			unauthorized = auth != "Bearer "+testBearerToken
		} else {
			unauthorized = !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") == ""
		}
		if unauthorized {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(client.ClmErrorResponse{})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleUserInfo(w http.ResponseWriter, _ *http.Request) {
	resp := client.UserInfoResponse{
		Sub:   "clm-test-user",
		Name:  "CLM Test Account",
		Email: "clm-test@example.com",
		Accounts: []client.AccountInfo{
			{
				AccountId:   AccountID,
				IsDefault:   true,
				AccountName: "CLM Test Account",
				BaseURI:     s.baseURL,
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleClmAccountDiscovery mocks CLM's account discovery endpoint (GET
// /api/v2/{accountId}/account on auth.springcm.com/authuat.springcm.com in production)
// — see clm_client.go's ensureClmInitialized and its package doc for why this exists
// and what's confirmed about it. The exact response schema wasn't available when this
// was written, so this returns the field name ensureClmInitialized checks first
// (ApiBaseUrl), matching the field name confirmed on CLM's legacy token-exchange
// response for the same concept.
func (s *Server) handleClmAccountDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{client.ClmDiscoveryFieldAPIBaseURL: s.baseURL})
}

// pageSlice applies pageSortParams.offset/limit to an ordered ID slice and returns the
// page's IDs plus the ClmPage metadata — shared by every List/Search handler.
// Returns (page IDs, page metadata).
func pageSlice(r *http.Request, ids []string) ([]string, client.ClmPage) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("pageSortParams.offset"))
	limit, err := strconv.Atoi(r.URL.Query().Get("pageSortParams.limit"))
	if err != nil || limit <= 0 {
		limit = client.DefaultPageSize
	}

	total := len(ids)
	end := offset + limit
	if end > total {
		end = total
	}
	if offset > total {
		offset = total
	}

	meta := client.ClmPage{Offset: offset, Limit: limit, Total: total}
	var page []string
	if offset < end {
		page = ids[offset:end]
	}
	return page, meta
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeNotFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(client.ClmErrorResponse{})
}
