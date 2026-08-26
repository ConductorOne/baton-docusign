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
//	POST  /v2/{accountId}/foldersearchtasks             — SearchFolders (create task; resolves inline)
//	GET   /v2/{accountId}/foldersearchtasks/{id}        — SearchFolders (poll a task — see PendingFolderSearchPolls)
//	GET   /v2/{accountId}/foldersearchtasks/{id}/result — SearchFolders (continuation pages)
//	GET   /v2/{accountId}/folders/{id}                  — GetFolder (supports ?expand=Security)
//	POST  /v2/{accountId}/changesecuritytasks           — PatchFolderSecurity (create task; resolves inline)
//	GET   /v2/{accountId}/changesecuritytasks/{id}      — PatchFolderSecurity (poll a task — see PendingChangeSecurityPolls)
//	GET   /v2/{accountId}/groups                        — ListGroups
//	GET   /v2/{accountId}/groups/{id}/groupmembers       — GetGroupMembers
//	GET   /v2/{accountId}/members                       — ListMembers
//	GET   /v2/{accountId}/members/{id}/groups            — GetMemberGroups (paginated)
//	PATCH /v2/{accountId}/members/{id}                  — PatchMemberGroups (additive)
//	PUT   /v2/{accountId}/members/{id}                  — PutMemberGroups (full-replace)
//	GET   /v2/{accountId}/permissionsets                 — ListPermissionSets
//	GET   /v2/{accountId}/members/{id}/workflowqueues    — GetMemberWorkflowQueues (paginated)
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

	workflowQueues       map[string]*client.ClmWorkflowQueue
	memberWorkflowQueues map[string][]string // memberID -> workflow queue IDs, seed-only (no write endpoint)

	memberGroupsRequests         int // count of GET .../members/{id}/groups calls, for pagination assertions
	memberWorkflowQueuesRequests int // count of GET .../members/{id}/workflowqueues calls, for pagination assertions

	forcedMemberWorkflowQueuesStatus map[string]int // memberID -> forced HTTP status, for tests

	lastPatchedMemberGroupHrefs map[string][]string // memberID -> the raw Href strings the last PATCH request body carried, for tests

	nextFolderSearchTaskID int // incrementing counter for mock FolderSearchTasks task IDs

	// pendingFolderSearchPolls, when > 0, makes the next SearchFolders task created via
	// POST /foldersearchtasks come back "Processing" this many times before resolving to
	// "Success" on poll — see SetPendingFolderSearchPolls. Decremented on each poll.
	pendingFolderSearchPolls int

	// omitFolderSearchResultHref, when true, leaves Result.Href empty on the next
	// successful folder-search task response — see SetOmitFolderSearchResultHref.
	omitFolderSearchResultHref bool

	// folderSearchTaskHrefOverride, when non-empty, replaces the Href on the next folder
	// search task response — see SetFolderSearchTaskHrefOverride.
	folderSearchTaskHrefOverride string

	nextChangeSecurityTaskID int // incrementing counter for mock ChangeSecurityTasks task IDs

	// pendingChangeSecurityPolls, when > 0, makes the next PatchFolderSecurity task
	// created via POST /changesecuritytasks come back "waiting" this many times before
	// resolving to "success" on poll — see SetPendingChangeSecurityPolls. Decremented on
	// each poll.
	pendingChangeSecurityPolls int
}

// ForceMemberWorkflowQueuesStatus makes GET .../members/{id}/workflowqueues fail with
// the given HTTP status for exactly this memberID — for tests that need a specific
// gRPC code (e.g. PermissionDenied/Unauthenticated) out of one particular member's
// call, distinct from the unknown-member 404 handleMemberWorkflowQueues already
// produces for an ID absent from the seed. Call after NewServer returns.
func (s *Server) ForceMemberWorkflowQueuesStatus(memberID string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forcedMemberWorkflowQueuesStatus[memberID] = status
}

// SetPendingFolderSearchPolls makes the next folder search task created by this server
// require n polls of GET .../foldersearchtasks/{id} before resolving to "Success" —
// exercises SearchFolders' awaitClmFolderSearchTask polling loop, which every live test
// against a real CLM tenant resolved past on the first try (inline in the POST
// response), leaving that branch otherwise untested.
func (s *Server) SetPendingFolderSearchPolls(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingFolderSearchPolls = n
}

// SetFolderSearchTaskHrefOverride makes the next folder search task response carry the
// given Href instead of this server's own — used to pin the client's host-validation
// guard (Client.validateClmURL) against a task Href pointing at an unexpected host.
func (s *Server) SetFolderSearchTaskHrefOverride(href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.folderSearchTaskHrefOverride = href
}

// SetOmitFolderSearchResultHref makes the next successful FolderSearchTasks response
// leave Result.Href empty. Used to pin SearchFolders' guard against minting a
// continuation token that would re-POST a new search (page-1 loop).
func (s *Server) SetOmitFolderSearchResultHref(omit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.omitFolderSearchResultHref = omit
}

// SetPendingChangeSecurityPolls makes the next change-security task created by this
// server require n polls of GET .../changesecuritytasks/{id} before resolving to
// "success" — exercises PatchFolderSecurity's awaitClmChangeSecurityTask polling loop,
// unverified against a live tenant (see PatchFolderSecurity's doc in clm_client.go).
func (s *Server) SetPendingChangeSecurityPolls(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingChangeSecurityPolls = n
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

// MemberWorkflowQueuesRequestCount is MemberGroupsRequestCount's equivalent for GET
// .../members/{id}/workflowqueues.
func (s *Server) MemberWorkflowQueuesRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memberWorkflowQueuesRequests
}

// AddBulkWorkflowQueueMember adds a new member (not part of the default seed) who
// belongs to queueCount newly created, distinct workflow queues — for pagination tests
// that need a member with more workflow-queue memberships than fit on one page, without
// perturbing the default seed's member count or distinct-queue count that other tests
// (both here and in pkg/connector) assert on. Call after NewServer returns.
func (s *Server) AddBulkWorkflowQueueMember(memberID string, queueCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	member := &client.ClmMember{Email: memberID + "@example.com", UserName: memberID}
	member.Href = s.MemberHref(memberID)
	s.members[memberID] = member
	s.memberOrder = append(s.memberOrder, memberID)

	queueIDs := make([]string, 0, queueCount)
	for i := 1; i <= queueCount; i++ {
		qid := fmt.Sprintf("queue-bulk-%03d", i)
		s.workflowQueues[qid] = &client.ClmWorkflowQueue{
			Name: fmt.Sprintf("Bulk Queue %03d", i),
			Href: s.WorkflowQueueHref(qid),
		}
		queueIDs = append(queueIDs, qid)
	}
	s.memberWorkflowQueues[memberID] = queueIDs
}

// AddMemberWithoutHref seeds a member with an empty Href — clmIDFromHref then reports an
// empty ID for it, exercising List()'s empty-memberID guard (a malformed record CLM's
// own API could plausibly return; this connector's endpoint shapes are
// documented-but-unexercised against a live tenant) without the scan ever reaching
// GetMemberWorkflowQueues for this member. Call after NewServer returns.
func (s *Server) AddMemberWithoutHref(memberID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[memberID] = &client.ClmMember{Email: memberID + "@example.com", UserName: memberID}
	s.memberOrder = append(s.memberOrder, memberID)
}

// AddMemberWorkflowQueueWithEmptyHref seeds a new member (not part of the default seed)
// with a single workflow-queue membership whose Href is empty — clmIDFromHref then
// reports an empty ID for it, exercising clm_workflow_queue.List()'s skip-unusable-ID
// guard for one queue within an otherwise normal member scan. Mirrors
// AddMemberWithoutHref's equivalent case at the member level. Call after NewServer
// returns.
func (s *Server) AddMemberWorkflowQueueWithEmptyHref(memberID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	member := &client.ClmMember{Email: memberID + "@example.com", UserName: memberID}
	member.Href = s.MemberHref(memberID)
	s.members[memberID] = member
	s.memberOrder = append(s.memberOrder, memberID)

	const queueID = "queue-no-href"
	s.workflowQueues[queueID] = &client.ClmWorkflowQueue{Name: "No Href Queue"} // Href intentionally empty
	s.memberWorkflowQueues[memberID] = []string{queueID}
}

// LastPatchedMemberGroupHrefs returns the raw Href strings the most recent PATCH
// .../members/{id} request body carried for memberID — unlike MemberGroups (which
// reduces everything to the trailing ID via idFromHref, the same as the real API's own
// comparison semantics), this exposes the exact Href the connector sent, so a test can
// tell a sample-derived Href from a base-URL-derived one even when the two mock helpers
// that build them happen to produce byte-identical strings.
func (s *Server) LastPatchedMemberGroupHrefs(memberID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lastPatchedMemberGroupHrefs[memberID]))
	copy(out, s.lastPatchedMemberGroupHrefs[memberID])
	return out
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

// SetGroupHref overrides the Href a seeded group reports on GetMemberGroups, letting a
// test make it observably different from what GroupHref (and so client.GroupHref's
// fallback derivation, which builds the same shape from the discovered base URL) would
// produce — needed to distinguish a sample-derived Href from a fallback-derived one when
// both would otherwise be byte-identical.
func (s *Server) SetGroupHref(id, href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.groups[id]; ok {
		g.Href = href
		return
	}
	if s.t != nil {
		//nolint:gocritic // s.t is testing.TB; ruleguard only exempts concrete *testing.T/B/F, not the interface
		s.t.Fatalf("SetGroupHref: no seeded group %q", id)
	}
}

// SetFolderGroupSecurityHref and SetFolderUserSecurityHref override the Href of an
// existing group/user security entry on a seeded folder (matched by ID via idFromHref),
// letting a test make it observably different from what client.GroupHref/MemberHref's
// fallback would produce for a different, derived ID — the same lever SetGroupHref gives
// GetMemberGroups-based tests, needed here because folder security entries carry no
// separate ID field of their own.
func (s *Server) SetFolderGroupSecurityHref(folderID, groupID, href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	folder, ok := s.folders[folderID]
	if ok {
		for i := range folder.Security.Groups {
			if idFromHref(folder.Security.Groups[i].Href) == groupID {
				folder.Security.Groups[i].Href = href
				return
			}
		}
	}
	if s.t != nil {
		//nolint:gocritic // s.t is testing.TB; ruleguard only exempts concrete *testing.T/B/F, not the interface
		s.t.Fatalf("SetFolderGroupSecurityHref: no group %q security entry on folder %q", groupID, folderID)
	}
}

func (s *Server) SetFolderUserSecurityHref(folderID, memberID, href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	folder, ok := s.folders[folderID]
	if ok {
		for i := range folder.Security.Users {
			if idFromHref(folder.Security.Users[i].Href) == memberID {
				folder.Security.Users[i].Href = href
				return
			}
		}
	}
	if s.t != nil {
		//nolint:gocritic // s.t is testing.TB; ruleguard only exempts concrete *testing.T/B/F, not the interface
		s.t.Fatalf("SetFolderUserSecurityHref: no member %q security entry on folder %q", memberID, folderID)
	}
}

func (s *Server) MemberHref(id string) string {
	return fmt.Sprintf("%s/v2/%s/members/%s", s.baseURL, AccountID, id)
}

func (s *Server) WorkflowQueueHref(id string) string {
	return fmt.Sprintf("%s/v2/%s/workflowqueues/%s", s.baseURL, AccountID, id)
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

// FolderSecurity returns a defensive copy of the current (test-visible) Security state
// for a folder — three separate collections by principal type, see
// client.ClmFolderSecurity's doc — for assertions after a Grant/Revoke round trip.
func (s *Server) FolderSecurity(folderID string) client.ClmFolderSecurity {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.folders[folderID]
	if !ok {
		return client.ClmFolderSecurity{}
	}
	groups := make([]client.ClmGroupSecurityEntry, len(f.Security.Groups))
	copy(groups, f.Security.Groups)
	roles := make([]client.ClmRoleSecurityEntry, len(f.Security.Roles))
	copy(roles, f.Security.Roles)
	users := make([]client.ClmUserSecurityEntry, len(f.Security.Users))
	copy(users, f.Security.Users)
	return client.ClmFolderSecurity{
		Groups: groups,
		Roles:  roles,
		Users:  users,
	}
}

// newState allocates the maps/slices a fresh Server needs — shared by NewServer and
// RunStandalone so both construct exactly the same seeded state.
func newState() *Server {
	return &Server{
		folders:                          make(map[string]*client.ClmFolder),
		groups:                           make(map[string]*client.ClmGroup),
		groupMembers:                     make(map[string][]string),
		members:                          make(map[string]*client.ClmMember),
		memberGroups:                     make(map[string][]string),
		permissionSets:                   make(map[string]*client.ClmPermissionSet),
		workflowQueues:                   make(map[string]*client.ClmWorkflowQueue),
		memberWorkflowQueues:             make(map[string][]string),
		forcedMemberWorkflowQueuesStatus: make(map[string]int),
		lastPatchedMemberGroupHrefs:      make(map[string][]string),
	}
}

// newMux registers every endpoint handler on a fresh mux — shared by NewServer and
// RunStandalone so both serve exactly the same routes.
func newMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/userinfo", s.handleUserInfo)
	mux.HandleFunc("GET /api/v2/{accountId}/account", s.requireAuth(s.handleClmAccountDiscovery))
	mux.HandleFunc("POST /v2/{accountId}/foldersearchtasks", s.requireAuth(s.handleCreateFolderSearchTask))
	mux.HandleFunc("GET /v2/{accountId}/foldersearchtasks/{id}", s.requireAuth(s.handlePollFolderSearchTask))
	mux.HandleFunc("GET /v2/{accountId}/foldersearchtasks/{id}/result", s.requireAuth(s.handleFolderSearchTaskResult))
	mux.HandleFunc("GET /v2/{accountId}/folders/{id}", s.requireAuth(s.handleGetFolder))
	mux.HandleFunc("POST /v2/{accountId}/changesecuritytasks", s.requireAuth(s.handleCreateChangeSecurityTask))
	mux.HandleFunc("GET /v2/{accountId}/changesecuritytasks/{id}", s.requireAuth(s.handlePollChangeSecurityTask))
	mux.HandleFunc("GET /v2/{accountId}/groups", s.requireAuth(s.handleListGroups))
	mux.HandleFunc("GET /v2/{accountId}/groups/{id}/groupmembers", s.requireAuth(s.handleGroupMembers))
	mux.HandleFunc("GET /v2/{accountId}/members", s.requireAuth(s.handleListMembers))
	mux.HandleFunc("GET /v2/{accountId}/members/{id}/groups", s.requireAuth(s.handleMemberGroups))
	mux.HandleFunc("GET /v2/{accountId}/members/{id}/workflowqueues", s.requireAuth(s.handleMemberWorkflowQueues))
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

// NewClientWithToken builds another *client.Client wired to this same running server
// (via the identical rewriteTransport, so it reaches the mock regardless of which real
// host it thinks it's talking to) but presenting an arbitrary bearer token instead of
// the fixed testBearerToken NewServer uses. Used to simulate an account/token that
// can't use CLM (wrong OAuth scope, no subscription) — requireAuth's strict mode
// rejects anything but the exact testBearerToken, so any other value exercises the
// connector's handling of a 401 from CLM.
func (s *Server) NewClientWithToken(token string) *client.Client {
	mockServerURL, _ := url.Parse(s.baseURL)
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})

	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	return client.NewClient(context.Background(), false, tokenSource, "", "", wrapper)
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
