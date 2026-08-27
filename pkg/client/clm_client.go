// Package client — DocuSign CLM (Contract Lifecycle Management) support.
//
// CLM is a separate DocuSign product from eSignature, on a different host, with its
// own OAuth scopes ("spring_read"/"spring_write", see oauth.go) and a different Object
// API surface. Endpoints below are derived from DocuSign's CLM API reference (method
// tables and request/response schemas). Validate against cmd/test-server during
// development. Live-tested end-to-end against a real CLM tenant (demo/UAT
// environment): Groups/Members/PermissionSets/Roles all synced correctly with their
// entitlements and grants, once the authorizing user had admin rights in that
// environment — an earlier 401 "Access Denied" on every data call (discovery
// succeeded) turned out to be exactly that, not a scope, licensing, or demo-vs-
// production issue as initially suspected. Folders required a separate fix — see
// SearchFolders' doc for the confirmed request/response shape.
//
// # API Endpoints Used
//
// Folders:
//   - POST /v2/{accountId}/foldersearchtasks - Search for folders (async Task API — see
//     SearchFolders' doc). Prefer this over the also-documented Folders:Search
//     (POST /v2/{accountId}/folders/search), which returns 405 live.
//   - GET  /v2/{accountId}/folders/{id}?expand=Security - Get a folder with its explicit security entries.
//     Security is three separate collections by principal type (Groups/Roles/Users), confirmed via
//     DocuSign's own Folders.Patch reference page - see ClmFolderSecurity's doc in clm_models.go.
//   - POST /v2/{accountId}/changesecuritytasks - Update folder security (grant: set an AccessType on
//     the relevant Groups/Roles/Users entry; revoke: set that entry's AccessType to "NoAccess") — see
//     PatchFolderSecurity's doc. NOT the generic Folders Patch: that endpoint accepts a Security field
//     in its documented schema but silently ignores it live (confirmed: 200 OK, no error, but a fresh
//     GET shows no change, while a trivial PATCH of another field on the same folder did apply). CLM's
//     own error code list names a distinct "136 - Missing Change Security Task", and the CLM API
//     Reference confirms ChangeSecurityTasks as its own Task API resource for this. This rewrite
//     matches ChangeSecurityTasks' documented request/response schema but is NOT independently
//     verified live — per explicit instruction, this project is done live-testing against the
//     customer's tenant.
//
// Groups:
//   - GET /v2/{accountId}/groups - List CLM groups (GetAllGroups)
//   - GET /v2/{accountId}/groups/{id}/groupmembers - List a group's members (GetUsers)
//
// Members (CLM's principal object):
//   - GET   /v2/{accountId}/members - List members (GetMembers)
//   - GET   /v2/{accountId}/members/{id}/groups - Groups a member belongs to
//   - GET   /v2/{accountId}/members/{id}/workflowqueues - Workflow queues a member belongs to
//   - PATCH /v2/{accountId}/members/{id} - Add member to new groups (additive/merge)
//   - PUT   /v2/{accountId}/members/{id} - Replace member's groups (adds new, removes unspecified)
//
// PermissionSets:
//   - GET /v2/{accountId}/permissionsets - List permission sets (GetAll). Confirmed read-only:
//     no assignment/grant endpoint exists anywhere in the CLM API.
//
// # Base URL resolution
//
// CLM's Object API base URL (a distinct host from eSignature's per-account base_uri,
// e.g. api.{site}.{region}.clm.docusign.net) is resolved via a separate account
// discovery call, confirmed via DocuSign's "CLM API 101" documentation:
//
//	GET https://auth.springcm.com/api/v2/{accountId}/account       (production)
//	GET https://authuat.springcm.com/api/v2/{accountId}/account    (demo/UAT)
//
// authenticated with the same Bearer access token this connector already obtains via
// the unified eSignature OAuth flow (account.docusign.com/account-d.docusign.com) — no
// separate SpringCM-specific authentication is required to call it, per that same
// documentation. This is a different, legacy-hosted mechanism from both the token
// response and /oauth/userinfo: neither of those was confirmed to carry the CLM base
// URL (the token response's documented fields are access_token/token_type/
// refresh_token/expires_in/scope; /oauth/userinfo's are sub/name/accounts[] with no
// CLM-specific field), and an earlier version of this code incorrectly read it from
// the token's Extra data as a result.
//
// This endpoint's exact response schema was not available when this was written —
// only the endpoint, its auth requirement, and its stated purpose ("account related
// URLs including the base CLM API URLs") were confirmed. ensureClmInitialized below
// checks a short list of the most likely field names and fails with the actual
// response's field names listed if none match, rather than guessing wrong silently.
//
// # Pagination
//
// Offset-based: pageSortParams.offset / pageSortParams.limit / pageSortParams.sortProperty /
// pageSortParams.sortDirection / pageSortParams.filter query params; responses wrap
// results as {First, Href, Items, Last, Limit, Next, Offset, Previous, Total}.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// clmAccountDiscoveryHostProd and clmAccountDiscoveryHostDemo are CLM's legacy
// (SpringCM-hosted) account discovery hosts — see the package doc's "Base URL
// resolution" section.
const (
	clmAccountDiscoveryHostProd = "https://auth.springcm.com"
	clmAccountDiscoveryHostDemo = "https://authuat.springcm.com"
)

// clmBaseURLCandidateFields lists the response field names most likely to carry the
// CLM Object API base URL, in priority order. ClmDiscoveryFieldAPIBaseURL mirrors the
// field name CLM's legacy token-exchange response uses for the same concept, per the
// CLM.CM OAuth 2.0 Web Server Flow documentation — exported so clmtest's mock can use
// the identical constant rather than a duplicated literal. Deliberately excludes
// generic names like "BaseUrl"/"base_url": the discovery response's exact schema
// isn't confirmed, and a bare "BaseUrl" is plausible enough as a name for an unrelated
// field (e.g. a web/UI URL) that matching it could silently misroute every CLM API
// call at the wrong host instead of failing loudly the way an absent field does.
const ClmDiscoveryFieldAPIBaseURL = "ApiBaseUrl"

var clmBaseURLCandidateFields = []string{
	ClmDiscoveryFieldAPIBaseURL, "api_base_url",
	"ObjectApiUrl", "object_api_url",
}

// CLM API endpoint constants.
const (
	// clmCreateFolderSearchTask creates a CLM FolderSearchTasks task — see
	// SearchFolders' doc. The CLM API Reference also documents a synchronous
	// Folders:Search at POST /v2/{accountId}/folders/search, but against a real
	// tenant that path returns 405 Method Not Allowed, so this connector uses the
	// documented async FolderSearchTasks resource instead.
	clmCreateFolderSearchTask = "/v2/%s/foldersearchtasks"
	// clmCreateChangeSecurityTask creates a CLM ChangeSecurityTasks task — see
	// PatchFolderSecurity's doc. The generic Folders Patch this replaced silently
	// ignores a Security payload (confirmed live: 200 OK, but no effect).
	clmCreateChangeSecurityTask = "/v2/%s/changesecuritytasks"
	clmGetFolder                = "/v2/%s/folders/%s"
	clmGetGroups                = "/v2/%s/groups"
	clmGetGroupMembers          = "/v2/%s/groups/%s/groupmembers"
	clmGetMembers               = "/v2/%s/members"
	clmGetMemberGroups          = "/v2/%s/members/%s/groups"
	clmPatchPutMember           = "/v2/%s/members/%s"
	clmGetPermissionSet         = "/v2/%s/permissionsets"
	clmGetMemberQueues          = "/v2/%s/members/%s/workflowqueues"

	// clmGroupPath and clmMemberPath are path *shapes*, not endpoints this connector
	// calls — hrefFor builds a Href string locally from these, issuing no request beyond
	// the one-time CLM base-URL discovery ensureClmReady may trigger, so neither
	// corresponds to an entry in this file's "API Endpoints Used" doc. clmMemberPath is
	// deliberately not clmPatchPutMember despite the identical shape: naming it after a
	// real PATCH/PUT endpoint would be just as misleading in the other direction.
	clmGroupPath  = "/v2/%s/groups/%s"
	clmMemberPath = "/v2/%s/members/%s"
)

// ensureClmInitialized resolves the CLM Object API base URL, separately from
// eSignature's ensureInitialized/baseURI, via CLM's account discovery endpoint — see
// the package doc's "Base URL resolution" section for the confirmed endpoint/auth and
// why the response field name is checked defensively rather than assumed.
func (c *Client) ensureClmInitialized(ctx context.Context) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.clmBaseURIReady {
		return nil
	}

	if c.clmBaseURLOverride != "" {
		c.clmBaseURI = c.clmBaseURLOverride
		c.clmBaseURIReady = true
		return nil
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("baton-docusign: failed to get token for CLM base URL resolution: %w", err)
	}

	discoveryHost := clmAccountDiscoveryHostProd
	if c.isDemo {
		discoveryHost = clmAccountDiscoveryHostDemo
	}
	// baseURLOverride (testing only, see config.BaseURLField) redirects every
	// eSignature/CLM host this connector talks to at a local mock — including this
	// discovery call's otherwise-hardcoded auth.springcm.com/authuat.springcm.com host.
	if c.baseURLOverride != "" {
		discoveryHost = c.baseURLOverride
	}
	discoveryURLStr, err := url.JoinPath(discoveryHost, "api", "v2", c.accountId, "account")
	if err != nil {
		return fmt.Errorf("baton-docusign: invalid CLM account discovery URL: %w", err)
	}
	discoveryURL, err := url.Parse(discoveryURLStr)
	if err != nil {
		return fmt.Errorf("baton-docusign: invalid CLM account discovery URL: %w", err)
	}

	request, err := c.wrapper.NewRequest(ctx, http.MethodGet, discoveryURL,
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(token.AccessToken),
	)
	if err != nil {
		return fmt.Errorf("baton-docusign: failed to build CLM account discovery request: %w", err)
	}

	var raw map[string]json.RawMessage
	if _, _, err := doRequestCommon(c.wrapper, request, &raw, &ClmErrorResponse{}); err != nil {
		return fmt.Errorf("baton-docusign: failed to discover the CLM API base URL: %w", err)
	}

	baseURL, ok := clmExtractBaseURLField(raw)
	if !ok {
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		return status.Errorf(codes.FailedPrecondition, "baton-docusign: CLM account discovery response at %s did not contain a recognized "+
			"base-URL field (checked %v); response contained these fields instead: %v", discoveryURL, clmBaseURLCandidateFields, keys)
	}

	c.clmBaseURI = baseURL
	c.clmBaseURIReady = true
	return nil
}

// EnsureClmReady exposes the CLM-readiness check every other CLM client method runs
// internally before its real request, for callers with no CLM endpoint of their own
// that still need to detect CLM availability — namely Connector.Validate() (see
// pkg/connector/connector.go), which runs this once, up front, before any CLM
// builder's List() executes. Memoized after the first successful call, same as every
// other CLM method — see ensureClmInitialized.
func (c *Client) EnsureClmReady(ctx context.Context) error {
	return c.ensureClmReady(ctx)
}

// clmExtractBaseURLField scans a CLM account discovery response for the first
// recognized base-URL field, in clmBaseURLCandidateFields priority order. Split out
// from ensureClmInitialized so this defensive-fallback logic can be unit tested
// directly against hand-built responses, without needing an HTTP mock.
func clmExtractBaseURLField(raw map[string]json.RawMessage) (string, bool) {
	for _, field := range clmBaseURLCandidateFields {
		fieldValue, ok := raw[field]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(fieldValue, &value); err == nil && value != "" {
			return value, true
		}
	}
	return "", false
}

// ensureClmReady runs both initialization checks every CLM method needs, in order.
func (c *Client) ensureClmReady(ctx context.Context) error {
	if err := c.ensureInitialized(ctx); err != nil {
		return err
	}
	return c.ensureClmInitialized(ctx)
}

// buildClmClientURL safely reads clmBaseURI and accountId to build a CLM URL.
// CLM reuses the same DocuSign accountId as eSignature — both are the same account,
// just different product licenses on it.
func (c *Client) buildClmClientURL(path string, params ...any) (*url.URL, error) {
	c.mutex.RLock()
	clmBaseURI := c.clmBaseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	return buildURL(clmBaseURI, path, clmPathParams(append([]any{accountId}, params...)...)...)
}

// clmPathParams applies url.PathEscape to every string path-segment before fmt.Sprintf
// inserts it into a CLM endpoint template (accountId, memberID, groupID, folderID, etc.).
func clmPathParams(params ...any) []any {
	out := make([]any, len(params))
	for i, p := range params {
		if s, ok := p.(string); ok {
			out[i] = url.PathEscape(s)
		} else {
			out[i] = p
		}
	}
	return out
}

// prepareClmPagedRequest safely prepares a paged CLM request URL. extra supplies any
// additional Sprintf placeholders in endpoint beyond accountId (e.g. a groupID/memberID
// path segment), in order. Returns what it requested alongside the URL — see
// preparePagedRequestClm's doc for why callers need this.
func (c *Client) prepareClmPagedRequest(endpoint string, options PageOptions, extra ...any) (*url.URL, clmRequestedPage, error) {
	c.mutex.RLock()
	clmBaseURI := c.clmBaseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	baseURL, err := url.Parse(clmBaseURI)
	if err != nil {
		return nil, clmRequestedPage{}, fmt.Errorf("baton-docusign: invalid CLM base URL: %w", err)
	}

	formatted := fmt.Sprintf(endpoint, clmPathParams(append([]any{accountId}, extra...)...)...)
	return preparePagedRequestClm(baseURL, formatted, options)
}

// clmKnownDomains are registrable domains CLM's own product is confirmed to use across
// its different hosts — see this file's package doc "Base URL resolution" section: the
// discovered Object API base URL is on *.clm.docusign.net, while account discovery is a
// separate, hardcoded auth.springcm.com/authuat.springcm.com host on a wholly different
// domain. Since CLM itself already spans two unrelated domains for different purposes,
// a Task API href on yet another CLM-owned host is plausible, which is why
// validateClmURL checks domain family rather than requiring the exact discovered host.
var clmKnownDomains = []string{"docusign.net", "springcm.com"}

// validateClmURL rejects a URL that isn't a plausible CLM host — a guard for the Task
// API polling/continuation URLs (task Href, SearchFolders' ResultHref) that come from a
// response body or a round-tripped page token rather than being built from clmBaseURI
// like every other CLM request. doClmRequest attaches this connector's bearer token to
// whatever URL it's given, with no host check of its own, so a malformed or tampered
// href here would otherwise send that token to an arbitrary host.
//
// Deliberately not an exact match against the discovered base host: clmPreferredHref's
// doc (pkg/connector/helper.go) notes CLM's Href host isn't guaranteed to match the
// discovered base URL, and this package's own confirmed base-URL-resolution flow proves
// it — the Object API base and the account-discovery host are already two different
// domains. An exact-host check would risk hard-failing every genuine Task API call (and
// so the whole clm_folder sync) the first time CLM legitimately serves one from a
// sibling host. source names the caller/field for the error message.
func (c *Client) validateClmURL(u *url.URL, source string) error {
	c.mutex.RLock()
	clmBaseURI := c.clmBaseURI
	c.mutex.RUnlock()
	base, err := url.Parse(clmBaseURI)
	if err != nil {
		return fmt.Errorf("baton-docusign: invalid CLM base URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) {
		return fmt.Errorf("baton-docusign: refusing to send CLM credentials to %s %q — expected scheme %q", source, u.String(), base.Scheme)
	}
	// Exact match compares the full authority (host+port): a --base-url/mock target like
	// http://127.0.0.1:5000 has no domain of its own to fall back on, so a same-host,
	// different-port href must still be rejected there. Hostname() (no port) is only
	// used in the domain-family loop below, where CLM's real hosts are all on 443.
	if strings.EqualFold(u.Host, base.Host) {
		return nil
	}
	host := u.Hostname()
	for _, domain := range clmKnownDomains {
		if strings.EqualFold(host, domain) || strings.HasSuffix(strings.ToLower(host), "."+domain) {
			return nil
		}
	}
	return fmt.Errorf("baton-docusign: refusing to send CLM credentials to %s %q — host %q is not a recognized CLM host", source, u.String(), host)
}

// doClmRequest executes an HTTP request against the CLM API and decodes the response.
// Mirrors Client.doRequest but targets the CLM host/error envelope.
func (c *Client) doClmRequest(ctx context.Context, method string, reqURL *url.URL, body any, response any, extraOpts ...uhttp.RequestOption) (annotations.Annotations, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, err
	}

	requestOptions := []uhttp.RequestOption{
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithBearerToken(token.AccessToken),
	}
	if body != nil {
		requestOptions = append(requestOptions, uhttp.WithJSONBody(body))
	}
	requestOptions = append(requestOptions, extraOpts...)

	request, err := c.wrapper.NewRequest(ctx, method, reqURL, requestOptions...)
	if err != nil {
		return nil, err
	}

	_, anno, err := doRequestCommon(c.wrapper, request, response, &ClmErrorResponse{})
	return anno, err
}

// clmMaxTaskPolls bounds how many times a CLM Task API poll loop (SearchFolders,
// PatchFolderSecurity) retries a not-yet-resolved task before giving up. Against a live
// CLM tenant, FolderSearchTasks always resolved inline (Status "Success" already in the
// POST response), so both polling branches are implemented per the Task API's
// documented contract but unverified live; the cap exists so a task that genuinely
// never resolves fails loudly instead of hanging.
const clmMaxTaskPolls = 30

// ClmFolderSearchTaskPollInterval is how long SearchFolders waits between polls of a
// "Processing" FolderSearchTasks task. Exported, like DefaultPageSize, so tests can
// override it — a real poll cadence would make a test exercising this branch
// needlessly slow.
var ClmFolderSearchTaskPollInterval = 2 * time.Second

// SearchFolders discovers folders via CLM's FolderSearchTasks (CLM API Reference →
// Tasks → FolderSearchTasks). Unlike Groups/Members/PermissionSets there is no
// flat list-all for folders. The Reference also documents Folders:Search
// (POST /v2/{accountId}/folders/search); this connector follows FolderSearchTasks
// because that sync path returns 405 live (see bullets below). A POST creates a
// search task, which either resolves inline or must be polled via its own Href
// until Status leaves "Processing", after which the paginated folder list is read
// from the task's Result. (FolderSearchTasks' Status field is an unenumerated
// string in the schema — live returns Title-Case "Success"/"Processing", distinct
// from ChangeSecurityTasks' documented lowercase success/waiting/failure/processing.)
//
// Confirmed live against a real CLM tenant:
//   - POST /v2/{accountId}/folders/search (Folders:Search in the Reference — this
//     function's original implementation) returns 405 Method Not Allowed, so the
//     documented sync search is not usable on the tenants we hit.
//   - POST /v2/{accountId}/foldersearchtasks requires a recognized search parameter in
//     the body — an empty body, or {"Name": ...} (the field ClmFolder's own JSON tag
//     uses), is rejected with CLM ErrorCode 1024 "no valid search parameter" against
//     every property name tried except "Title". {"Title": ""} is accepted and matches
//     every folder (Title is a substring match, so empty matches everything) —
//     confirmed against a real account with 100 folders. Title is a documented
//     FolderSearchTask request field in the Reference.
//   - The task resolved inline (Status "Success" already in the POST response, Result
//     already populated) on every live test; the "Processing" polling branch below is
//     unverified live.
//   - Continuation pages don't re-POST a new search: they GET the task's own Result
//     href (offset/limit appended) directly, confirmed live to return the same flat,
//     paginated shape as every other CLM list endpoint.
func (c *Client) SearchFolders(ctx context.Context, options PageOptions) ([]ClmFolder, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	if options.PageToken != "" {
		decoded, err := decodeClmPageToken(options.PageToken)
		if err != nil {
			return nil, "", nil, fmt.Errorf("baton-docusign: invalid CLM page token: %w", err)
		}
		if decoded.ResultHref == "" {
			// A SearchFolders continuation token without ResultHref would fall through
			// to POST a brand-new search task (page 1 again) — an unbounded loop when
			// more pages remain. Fail loud instead.
			return nil, "", nil, fmt.Errorf("baton-docusign: CLM folder search page token missing ResultHref")
		}
		return c.getClmFolderSearchResultPage(ctx, decoded.ResultHref, options)
	}

	createURL, err := c.buildClmClientURL(clmCreateFolderSearchTask)
	if err != nil {
		return nil, "", nil, err
	}
	// Page-1 unconfirmed live: FolderSearchTasks' create response embeds a page of
	// results inline (see this func's doc), and every other paged CLM request controls
	// its page size via pageSortParams.limit on the request URL — applying the same
	// convention here on the POST, rather than leaving page 1 to whatever CLM's default
	// happens to be, since options.PageSize should govern the first page like it does
	// every continuation page (getClmFolderSearchResultPage).
	createURL, requestedPage, err := appendClmPageQuery(createURL, options)
	if err != nil {
		return nil, "", nil, err
	}

	var task ClmFolderSearchTaskResponse
	anno, err := c.doClmRequest(ctx, http.MethodPost, createURL, map[string]string{"Title": ""}, &task)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to create CLM folder search task: %w", err)
	}

	task, err = c.awaitClmFolderSearchTask(ctx, task)
	if err != nil {
		return nil, "", anno, err
	}
	if task.Result == nil {
		return nil, "", anno, fmt.Errorf("baton-docusign: CLM folder search task %s succeeded with no Result", task.Href)
	}
	// Prefer the smaller of the requested and echoed page sizes: applying
	// pageSortParams.limit to the create POST is unconfirmed live (see this func's
	// doc), so either direction of mismatch is possible. An echoed Limit smaller than
	// requested (CLM ignored the param, served its own default) would make a
	// genuinely full page look short if left at the requested size; an echoed Limit
	// larger than requested (e.g. CLM's max page size rather than what it actually
	// applied) would make a genuinely full page look short the other way if trusted
	// outright. Taking the minimum is safe either way — worst case it costs one extra
	// empty-page request, never lost data.
	if task.Result.Limit > 0 && task.Result.Limit < requestedPage.PageSize {
		requestedPage.PageSize = task.Result.Limit
	}

	nextToken, err := getClmNextToken(requestedPage, len(task.Result.Items), task.Result.Next != "", task.Result.Total, task.Result.Href)
	if err != nil {
		return nil, "", anno, err
	}
	// getClmNextToken will happily mint a token with ResultHref:"" when Href is empty.
	// The next SearchFolders call would then re-POST (Requests reset to 0) and loop on
	// page 1 forever — maxClmListPages never fires. Refuse to emit that token.
	if nextToken != "" && task.Result.Href == "" {
		return nil, "", anno, fmt.Errorf("baton-docusign: CLM folder search task %s Result has no Href; cannot continue pagination", task.Href)
	}
	return task.Result.Items, nextToken, anno, nil
}

// getClmFolderSearchResultPage fetches one continuation page of an already-completed
// CLM folder search — see SearchFolders' doc for why this reads from a server-issued
// Result href instead of re-POSTing a new search task.
func (c *Client) getClmFolderSearchResultPage(ctx context.Context, resultHref string, options PageOptions) ([]ClmFolder, string, annotations.Annotations, error) {
	base, err := url.Parse(resultHref)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: invalid CLM folder search result href %q: %w", resultHref, err)
	}
	if err := c.validateClmURL(base, "folder search result href"); err != nil {
		return nil, "", nil, err
	}
	pageURL, requestedPage, err := appendClmPageQuery(base, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmFolderPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, pageURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to read CLM folder search results: %w", err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, resultHref)
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// awaitClmFolderSearchTask polls a CLM FolderSearchTasks task until it leaves
// "Processing", per the CLM Task API's documented contract (GET the task's own Href;
// Status becomes "Success" or "Failure") — see SearchFolders' doc for why this branch
// is unverified against a live tenant.
func (c *Client) awaitClmFolderSearchTask(ctx context.Context, task ClmFolderSearchTaskResponse) (ClmFolderSearchTaskResponse, error) {
	for attempt := 0; task.Status == "Processing"; attempt++ {
		if attempt >= clmMaxTaskPolls {
			return task, fmt.Errorf("baton-docusign: CLM folder search task %s did not finish after %d polls", task.Href, clmMaxTaskPolls)
		}
		select {
		case <-ctx.Done():
			return task, ctx.Err()
		case <-time.After(ClmFolderSearchTaskPollInterval):
		}

		pollURL, err := url.Parse(task.Href)
		if err != nil {
			return task, fmt.Errorf("baton-docusign: invalid CLM folder search task href %q: %w", task.Href, err)
		}
		if err := c.validateClmURL(pollURL, "folder search task href"); err != nil {
			return task, err
		}
		// WithNoCache: repeated GETs to the same task URL must observe its latest
		// Status, not a memoized first response — see GetFolder's noCache param for the
		// same uhttp GET-cache staleness concern.
		if _, err := c.doClmRequest(ctx, http.MethodGet, pollURL, nil, &task, uhttp.WithNoCache()); err != nil {
			return task, fmt.Errorf("baton-docusign: failed to poll CLM folder search task %s: %w", task.Href, err)
		}
	}
	if task.Status == "Failure" {
		return task, fmt.Errorf("baton-docusign: CLM folder search task %s failed", task.Href)
	}
	return task, nil
}

// GetFolder fetches a single folder, optionally expanding Security to get its explicit
// (non-inherited) folder security entries. The response may be served from the shared
// HTTP GET cache.
func (c *Client) GetFolder(ctx context.Context, folderID string, expand ...string) (*ClmFolder, annotations.Annotations, error) {
	return c.getFolder(ctx, folderID, false, expand...)
}

// GetFolderFresh is identical to GetFolder but bypasses the shared HTTP GET cache. Use
// this for the read-before-write check in Grant/Revoke: PatchFolderSecurity doesn't
// invalidate the cache entry a prior GetFolder call may have left behind, so a cached
// read there would see security entries as they stood before the most recent write
// rather than the current state.
func (c *Client) GetFolderFresh(ctx context.Context, folderID string, expand ...string) (*ClmFolder, annotations.Annotations, error) {
	return c.getFolder(ctx, folderID, true, expand...)
}

func (c *Client) getFolder(ctx context.Context, folderID string, noCache bool, expand ...string) (*ClmFolder, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, nil, err
	}

	folderURL, err := c.buildClmClientURL(clmGetFolder, folderID)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range expand {
		ApplyQueryParam(folderURL, "expand", e)
	}

	var extraOpts []uhttp.RequestOption
	if noCache {
		extraOpts = append(extraOpts, uhttp.WithNoCache())
	}

	var folder ClmFolder
	anno, err := c.doClmRequest(ctx, http.MethodGet, folderURL, nil, &folder, extraOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: failed to get CLM folder %s: %w", folderID, err)
	}

	return &folder, anno, nil
}

// PatchFolderSecurity grants or revokes folder security. write must be the folder's
// complete security state across all three principal-type collections (see the
// connector-layer caller — clmFolderSecurityToWrite builds this from a fresh read,
// with one entry changed/added in whichever of Groups/Roles/Users the grant/revoke
// targets), not just the one entry being changed: ChangeSecurityTasks' merge-vs-replace
// semantics for the Security field are undocumented, and sending the complete state
// is correct under either interpretation, whereas sending only the one changed entry
// would wipe every other principal's access to the folder if the real API replaces
// rather than merges.
//
// Sets a folder's security via CLM's ChangeSecurityTasks — a dedicated async Task API
// endpoint, NOT the generic Folders Patch this function used to call. Confirmed live
// that the generic PATCH /v2/{accountId}/folders/{id} silently ignores a Security
// payload entirely (200 OK, no error, but a fresh GET shows no change — a trivial PATCH
// of another field on the same folder DID apply and bump UpdatedDate, isolating the
// failure to Security specifically); CLM's own error code list separately names
// "136 - Missing Change Security Task", and the CLM API Reference confirms a distinct
// ChangeSecurityTasks resource ("Post: Set the security on a folder") exists for
// exactly this. See clm_client.go's package doc "Folders" section for the live
// evidence that led here.
//
// The request body shape (ClmChangeSecurityTaskRequest) is confirmed via the CLM API
// Reference's interactive schema browser, expanded past its default collapsed view —
// the target folder's Href and its new Security both nest under a Folder field; a
// same-shaped top-level Href/Security/Status on the schema are generic Task-wrapper
// fields this doc-generation tool reuses across every Task type, not what
// ChangeSecurityTasks actually reads for a folder security change (an earlier version
// of this code got this wrong from the same page's default collapsed view, which looks
// identical to a flat {Href, Security} pair until "Folder" is expanded).
//
// NOT independently verified live: per explicit instruction, this project is done
// live-testing against the customer's tenant. ChangeSecurityTasks' Status values are
// documented in lowercase (success/waiting/failure/processing) — confirmed distinct
// from FolderSearchTasks' PascalCase (Success/Processing), not a documentation
// inconsistency to normalize away — is the other thing worth a second look if this
// doesn't work in practice.
func (c *Client) PatchFolderSecurity(ctx context.Context, folderID string, write ClmFolderSecurityWrite) (annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, err
	}

	folderURL, err := c.buildClmClientURL(clmGetFolder, folderID)
	if err != nil {
		return nil, err
	}

	createURL, err := c.buildClmClientURL(clmCreateChangeSecurityTask)
	if err != nil {
		return nil, err
	}

	body := ClmChangeSecurityTaskRequest{Folder: ClmChangeSecurityTaskFolder{Href: folderURL.String(), Security: write}}

	var task ClmChangeSecurityTaskResponse
	anno, err := c.doClmRequest(ctx, http.MethodPost, createURL, body, &task)
	if err != nil {
		return anno, fmt.Errorf("baton-docusign: failed to create CLM change-security task for folder %s: %w", folderID, err)
	}

	task, err = c.awaitClmChangeSecurityTask(ctx, task)
	if err != nil {
		return anno, err
	}
	if task.Status != ClmChangeSecurityStatusSuccess {
		return anno, fmt.Errorf("baton-docusign: CLM change-security task %s for folder %s did not succeed (status %q)", task.Href, folderID, task.Status)
	}

	return anno, nil
}

// clmChangeSecurityStatus* are ChangeSecurityTasks' documented Status values —
// lowercase, distinct from FolderSearchTasks' PascalCase. See PatchFolderSecurity's doc.
const (
	ClmChangeSecurityStatusSuccess    = "success"
	ClmChangeSecurityStatusWaiting    = "waiting"
	ClmChangeSecurityStatusFailure    = "failure"
	ClmChangeSecurityStatusProcessing = "processing"
)

// awaitClmChangeSecurityTask polls a CLM ChangeSecurityTasks task until it leaves
// "waiting"/"processing" — mirrors awaitClmFolderSearchTask's polling loop, but against
// ChangeSecurityTasks' distinct (lowercase) Status vocabulary and leaner response shape
// (no Result field: this task mutates rather than returns data, so success/failure is
// the only thing to observe). Unverified live — see PatchFolderSecurity's doc.
func (c *Client) awaitClmChangeSecurityTask(ctx context.Context, task ClmChangeSecurityTaskResponse) (ClmChangeSecurityTaskResponse, error) {
	for attempt := 0; task.Status == ClmChangeSecurityStatusWaiting || task.Status == ClmChangeSecurityStatusProcessing; attempt++ {
		if attempt >= clmMaxTaskPolls {
			return task, fmt.Errorf("baton-docusign: CLM change-security task %s did not finish after %d polls", task.Href, clmMaxTaskPolls)
		}
		select {
		case <-ctx.Done():
			return task, ctx.Err()
		case <-time.After(ClmFolderSearchTaskPollInterval):
		}

		pollURL, err := url.Parse(task.Href)
		if err != nil {
			return task, fmt.Errorf("baton-docusign: invalid CLM change-security task href %q: %w", task.Href, err)
		}
		if err := c.validateClmURL(pollURL, "change-security task href"); err != nil {
			return task, err
		}
		// WithNoCache: see awaitClmFolderSearchTask's identical comment — repeated polls
		// of the same task URL must observe its latest Status, not a memoized first one.
		if _, err := c.doClmRequest(ctx, http.MethodGet, pollURL, nil, &task, uhttp.WithNoCache()); err != nil {
			return task, fmt.Errorf("baton-docusign: failed to poll CLM change-security task %s: %w", task.Href, err)
		}
	}
	return task, nil
}

// ListGroups lists CLM groups (a distinct object from eSignature groups).
//
// Pagination: offset/limit, see package doc.
func (c *Client) ListGroups(ctx context.Context, options PageOptions) ([]ClmGroup, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	listURL, requestedPage, err := c.prepareClmPagedRequest(clmGetGroups, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmGroupPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, listURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list CLM groups: %w", err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, "")
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// hrefFor builds an object's Href from its native ID and the resolved CLM base URL,
// shared by GroupHref and MemberHref, for callers that only have a ResourceId (no
// hydrated Resource to read a Href from). Assumes the shape "/v2/{account}/{collection}/{id}";
// unverified against a live tenant. Builds the string locally — issues no request beyond
// the one-time CLM base-URL discovery ensureClmReady may trigger.
func (c *Client) hrefFor(ctx context.Context, pathShape, id string) (string, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return "", err
	}
	objURL, err := c.buildClmClientURL(pathShape, id)
	if err != nil {
		return "", err
	}
	return objURL.String(), nil
}

// GroupHref builds a CLM group's Href from its native ID — see hrefFor's doc.
func (c *Client) GroupHref(ctx context.Context, groupID string) (string, error) {
	return c.hrefFor(ctx, clmGroupPath, groupID)
}

// MemberHref builds a CLM member's Href from its native ID — see hrefFor's doc.
func (c *Client) MemberHref(ctx context.Context, memberID string) (string, error) {
	return c.hrefFor(ctx, clmMemberPath, memberID)
}

// GetGroupMembers lists the members of a CLM group.
//
// Pagination: offset/limit, see package doc.
func (c *Client) GetGroupMembers(ctx context.Context, groupID string, options PageOptions) ([]ClmMember, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	membersURL, requestedPage, err := c.prepareClmPagedRequest(clmGetGroupMembers, options, groupID)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmMemberPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, membersURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list members of CLM group %s: %w", groupID, err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, "")
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// ListMembers lists CLM members (the CLM API's principal object). Synced as its own
// resource type rather than reused as the existing `user` resource, since identity
// between the two could not be confirmed 1:1.
//
// Pagination: offset/limit, see package doc.
func (c *Client) ListMembers(ctx context.Context, options PageOptions) ([]ClmMember, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	listURL, requestedPage, err := c.prepareClmPagedRequest(clmGetMembers, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmMemberPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, listURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list CLM members: %w", err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, "")
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// clmMaxMemberSubResourcePages bounds every "fetch a member's complete X" loop below
// (GetMemberGroups, GetMemberWorkflowQueues) in case the CLM API ever echoes a
// non-advancing Offset/Limit, which would make getClmNextToken compute the same "next"
// token forever. A member with more pages of groups/queues than this is implausible; if
// it ever happens, fail loudly instead of hanging.
const clmMaxMemberSubResourcePages = 1000

// clmPageToCompletion pages through fetchPage until it returns an empty nextPageToken,
// accumulating every item — the shared shape behind every "get a member's complete X"
// client method (GetMemberGroups, GetMemberWorkflowQueues), which each need the whole
// set at once rather than one page per caller-visible call. See GetMemberGroups' doc for
// why looping internally is an intentional carve-out from the usual client-layer rule.
// Bounded by maxPages and guards against a non-advancing token, so it can't hang even if
// the underlying assumption about the API is wrong.
func clmPageToCompletion[T any](fetchPage func(pageToken string) ([]T, string, annotations.Annotations, error), maxPages int, label string) ([]T, annotations.Annotations, error) {
	var all []T
	var anno annotations.Annotations
	pageToken := ""

	for i := 0; i < maxPages; i++ {
		page, nextPageToken, pageAnno, err := fetchPage(pageToken)
		if err != nil {
			return nil, anno, err
		}
		anno = append(anno, pageAnno...)
		all = append(all, page...)

		if nextPageToken == "" {
			return all, anno, nil
		}
		if nextPageToken == pageToken {
			return nil, anno, fmt.Errorf("baton-docusign: CLM API returned a non-advancing pagination token while listing %s", label)
		}
		pageToken = nextPageToken
	}

	return nil, anno, fmt.Errorf("baton-docusign: exceeded %d pages while listing %s", maxPages, label)
}

// GetMemberGroups gets the FULL current list of groups a member belongs to — required
// before Grant/Revoke, since both are read-modify-write against this list (Patch is
// additive/merge, Put is full-replace). This method pages to completion internally:
// callers need the complete list, not one page of it — Revoke in particular does a
// full-replace Put using this result, so a truncated list here would silently drop the
// member's memberships in every group beyond the first page.
//
// Intentional carve-out from the usual client-layer rule against looping through pages
// internally (that's normally the connector layer's job, driving one page per call):
// this isn't a sync List — it's a read-before-write for provisioning, where the caller
// fundamentally needs the complete set to safely do a full-replace Put, not a page at a
// time. See clmPageToCompletion for the shared bounded/non-advancing-token-guarded loop.
func (c *Client) GetMemberGroups(ctx context.Context, memberID string) ([]ClmGroup, annotations.Annotations, error) {
	return clmPageToCompletion(func(pageToken string) ([]ClmGroup, string, annotations.Annotations, error) {
		return c.getMemberGroupsPage(ctx, memberID, PageOptions{PageToken: pageToken})
	}, clmMaxMemberSubResourcePages, fmt.Sprintf("groups for member %s", memberID))
}

// getMemberGroupsPage fetches a single page of a member's group memberships. Both of
// GetMemberGroups' callers (Grant/Revoke) use it as a read-before-write immediately
// before a Patch/Put to the same member's groups, so this bypasses the shared HTTP GET
// cache: Patch/Put don't invalidate a prior cached read, and a cached response here
// would go stale the moment a Grant or Revoke writes to it — see GetFolderFresh's doc
// for the same issue on the folder-security path.
func (c *Client) getMemberGroupsPage(ctx context.Context, memberID string, options PageOptions) ([]ClmGroup, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	groupsURL, requestedPage, err := c.prepareClmPagedRequest(clmGetMemberGroups, options, memberID)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmGroupPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, groupsURL, nil, &page, uhttp.WithNoCache())
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to get groups for CLM member %s: %w", memberID, err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, "")
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// PatchMemberGroups grants group membership: the CLM API adds the member to any group
// in newGroups they don't already belong to and leaves the rest of their membership
// untouched (additive/merge — confirmed via the CLM API's own Patch description: "the
// member will be added to any new groups provided"). Since the merge happens API-side,
// callers may pass either just the group(s) being added or their full current list plus
// the addition — both are safe. This connector's only caller passes the full list.
func (c *Client) PatchMemberGroups(ctx context.Context, memberID string, newGroups []ClmGroup) (annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, err
	}

	memberURL, err := c.buildClmClientURL(clmPatchPutMember, memberID)
	if err != nil {
		return nil, err
	}

	body := ClmMemberGroupsPatch{Groups: ClmGroupList{Items: newGroups}}
	anno, err := c.doClmRequest(ctx, http.MethodPatch, memberURL, body, nil)
	if err != nil {
		return anno, fmt.Errorf("baton-docusign: failed to add CLM member %s to group(s): %w", memberID, err)
	}

	return anno, nil
}

// PutMemberGroups revokes group membership: replaces the member's full group list with
// fullGroups (any group omitted from this list is removed — confirmed via the CLM
// API's own Put description: "added to any new groups provided and removed from
// unspecified groups"). Callers must pass the member's CURRENT groups minus the one(s)
// being revoked, not just the groups to add — this is a full-replace, not a diff.
func (c *Client) PutMemberGroups(ctx context.Context, memberID string, fullGroups []ClmGroup) (annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, err
	}

	memberURL, err := c.buildClmClientURL(clmPatchPutMember, memberID)
	if err != nil {
		return nil, err
	}

	body := ClmMemberGroupsPatch{Groups: ClmGroupList{Items: fullGroups}}
	anno, err := c.doClmRequest(ctx, http.MethodPut, memberURL, body, nil)
	if err != nil {
		return anno, fmt.Errorf("baton-docusign: failed to update CLM member %s group list: %w", memberID, err)
	}

	return anno, nil
}

// ListPermissionSets lists CLM permission sets. Confirmed read-only — no
// grant/revoke/assignment endpoint exists anywhere in the CLM API for this object.
//
// Pagination: offset/limit, see package doc.
func (c *Client) ListPermissionSets(ctx context.Context, options PageOptions) ([]ClmPermissionSet, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	listURL, requestedPage, err := c.prepareClmPagedRequest(clmGetPermissionSet, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmPermissionSetPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, listURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list CLM permission sets: %w", err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, "")
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// GetMemberWorkflowQueues lists the workflow queues a CLM member belongs to. Like
// GetMemberGroups, this fetches the member's complete set rather than exposing a page
// token: clm_workflow_queue's List() (pkg/connector/clm_workflow_queues.go) needs the
// complete set for the member it was called with, not one page at a time.
// Confirmed read-only intent per the API's documented surface: there is no reverse
// lookup (queue to members) and no membership grant/revoke endpoint, only work-item
// assign/unassign — which this connector doesn't sync (see clm_workflow_queues.go).
func (c *Client) GetMemberWorkflowQueues(ctx context.Context, memberID string) ([]ClmWorkflowQueue, annotations.Annotations, error) {
	return clmPageToCompletion(func(pageToken string) ([]ClmWorkflowQueue, string, annotations.Annotations, error) {
		return c.getMemberWorkflowQueuesPage(ctx, memberID, PageOptions{PageToken: pageToken})
	}, clmMaxMemberSubResourcePages, fmt.Sprintf("workflow queues for member %s", memberID))
}

func (c *Client) getMemberWorkflowQueuesPage(ctx context.Context, memberID string, options PageOptions) ([]ClmWorkflowQueue, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	queuesURL, requestedPage, err := c.prepareClmPagedRequest(clmGetMemberQueues, options, memberID)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmWorkflowQueuePage
	anno, err := c.doClmRequest(ctx, http.MethodGet, queuesURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to get workflow queues for CLM member %s: %w", memberID, err)
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total, "")
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}
