// Package client — DocuSign CLM (Contract Lifecycle Management) support.
//
// CLM is a separate DocuSign product from eSignature, on a different host, with its
// own OAuth scopes ("spring_read"/"spring_write", see oauth.go) and a different Object
// API surface. Endpoints below are derived from DocuSign's CLM API reference (method
// tables and request/response schemas). Validate against cmd/test-server during
// development; a production CLM tenant was not available to exercise this integration
// directly.
//
// # API Endpoints Used
//
// Folders:
//   - POST /v2/{accountId}/folders/search - Discover folders (no flat list-all exists)
//   - GET  /v2/{accountId}/folders/{id}?expand=Security - Get a folder with its explicit security entries.
//     Security is three separate collections by principal type (Groups/Roles/Users), confirmed via
//     DocuSign's own Folders.Patch reference page - see ClmFolderSecurity's doc in clm_models.go.
//   - PATCH /v2/{accountId}/folders/{id} - Update folder security (grant: set an AccessType on the
//     relevant Groups/Roles/Users entry; revoke: set that entry's AccessType to "NoAccess")
//
// Groups:
//   - GET /v2/{accountId}/groups - List CLM groups (GetAllGroups)
//   - GET /v2/{accountId}/groups/{id}/groupmembers - List a group's members (GetUsers)
//
// Members (CLM's principal object):
//   - GET   /v2/{accountId}/members - List members (GetMembers)
//   - GET   /v2/{accountId}/members/{id}/groups - Groups a member belongs to
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

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
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
	clmSearchFolders    = "/v2/%s/folders/search"
	clmGetFolder        = "/v2/%s/folders/%s"
	clmPatchFolder      = "/v2/%s/folders/%s"
	clmGetGroups        = "/v2/%s/groups"
	clmGetGroupMembers  = "/v2/%s/groups/%s/groupmembers"
	clmGetMembers       = "/v2/%s/members"
	clmGetMemberGroups  = "/v2/%s/members/%s/groups"
	clmPatchPutMember   = "/v2/%s/members/%s"
	clmGetPermissionSet = "/v2/%s/permissionsets"
	clmGetMemberQueues  = "/v2/%s/members/%s/workflowqueues"
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
		// codes.FailedPrecondition (not a bare error, which status.Code() would read as
		// codes.Unknown): a non-CLM account's discovery response plausibly has a
		// different shape entirely (e.g. a bare account object with none of the
		// candidate fields), so isOptInFeatureUnavailableError needs a recognizable
		// code to tolerate this specific failure the same way it tolerates 401/403 —
		// see that function's doc in helper.go.
		return status.Errorf(codes.FailedPrecondition, "baton-docusign: CLM account discovery response at %s did not contain a recognized "+
			"base-URL field (checked %v); response contained these fields instead: %v", discoveryURL, clmBaseURLCandidateFields, keys)
	}

	c.clmBaseURI = baseURL
	c.clmBaseURIReady = true
	return nil
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

	return buildURL(clmBaseURI, path, append([]any{accountId}, params...)...)
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

	formatted := fmt.Sprintf(endpoint, append([]any{accountId}, extra...)...)
	return preparePagedRequestClm(baseURL, formatted, options)
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

// SearchFolders discovers folders via the CLM Folders search endpoint (there is no
// flat list-all endpoint for folders, unlike Groups/Members/PermissionSets).
//
// Pagination: offset/limit, see package doc.
func (c *Client) SearchFolders(ctx context.Context, options PageOptions) ([]ClmFolder, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	searchURL, requestedPage, err := c.prepareClmPagedRequest(clmSearchFolders, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmFolderPage
	anno, err := c.doClmRequest(ctx, http.MethodPost, searchURL, struct{}{}, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to search CLM folders: %w", err)
	}

	// The request body is empty (no search criteria) because the schema for scoping
	// this search to "all folders" was never confirmed against a live CLM tenant. If
	// that empty body means "no criteria -> no matches" rather than "match all", the
	// very first page would come back empty and every folder/folder-security sync
	// would silently report success while syncing zero folders. Surface that
	// possibility in the logs rather than fail silently, without treating it as a
	// hard error since an account with genuinely zero folders is also a valid state.
	if requestedPage.Offset == 0 && len(page.Items) == 0 {
		ctxzap.Extract(ctx).Debug("baton-docusign: CLM folder search returned zero results on the first page; " +
			"if this account has CLM folders, this may indicate the empty search body is being interpreted as " +
			"'no criteria -> no matches' rather than 'match all' — please report this to ConductorOne")
	}

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
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
// targets), not just the one entry being changed: Folders.Patch's merge-vs-replace
// semantics for the Security field are undocumented, and sending the complete state
// is correct under either interpretation, whereas sending only the one changed entry
// would wipe every other principal's access to the folder if the real API replaces
// rather than merges.
//
// UNVERIFIED against a real CLM tenant, and higher-risk than the merge-semantics
// question above: DocuSign's docs also reference a separate `ChangeSecurityTasks`
// resource (POST /v2/{accountId}/changesecuritytasks), which may be the documented
// way to mutate folder security asynchronously rather than a direct PATCH on the
// folder itself. If that's the actual contract, this method - the entire mechanism
// Grant/Revoke on clm_folder relies on - could silently no-op or 404 against a real
// tenant. Verify which endpoint the real API expects before treating folder
// provisioning here as more than best-effort.
func (c *Client) PatchFolderSecurity(ctx context.Context, folderID string, write ClmFolderSecurityWrite) (annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, err
	}

	folderURL, err := c.buildClmClientURL(clmPatchFolder, folderID)
	if err != nil {
		return nil, err
	}

	body := ClmFolderSecurityPatch{Security: write}
	anno, err := c.doClmRequest(ctx, http.MethodPatch, folderURL, body, nil)
	if err != nil {
		return anno, fmt.Errorf("baton-docusign: failed to update CLM folder %s security: %w", folderID, err)
	}

	return anno, nil
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

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
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

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
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

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// GetMemberGroups gets the FULL current list of groups a member belongs to — required
// before Grant/Revoke, since both are read-modify-write against this list (Patch is
// additive/merge, Put is full-replace). This method pages to completion internally:
// callers need the complete list, not one page of it — Revoke in
// particular does a full-replace Put using this result, so a truncated list here would
// silently drop the member's memberships in every group beyond the first page.
//
// Intentional carve-out from the usual client-layer rule against looping through pages
// internally (that's normally the connector layer's job, driving one page per call):
// this isn't a sync List — it's a read-before-write for provisioning, where the caller
// fundamentally needs the complete set to safely do a full-replace Put, not a page at a
// time. The loop is bounded (maxMemberGroupPages) and guards against a non-advancing
// token, so it can't hang even if the underlying assumption about the API is wrong.
func (c *Client) GetMemberGroups(ctx context.Context, memberID string) ([]ClmGroup, annotations.Annotations, error) {
	// maxMemberGroupPages bounds this loop in case the CLM API ever echoes a
	// non-advancing Offset/Limit, which would make getClmNextToken compute the same
	// "next" token forever. A member with more pages of groups than this is
	// implausible; if it ever happens, fail loudly instead of hanging.
	const maxMemberGroupPages = 1000

	var all []ClmGroup
	var anno annotations.Annotations
	pageToken := ""

	for i := 0; i < maxMemberGroupPages; i++ {
		page, nextPageToken, pageAnno, err := c.getMemberGroupsPage(ctx, memberID, PageOptions{PageToken: pageToken})
		if err != nil {
			return nil, anno, err
		}
		anno = append(anno, pageAnno...)
		all = append(all, page...)

		if nextPageToken == "" {
			return all, anno, nil
		}
		if nextPageToken == pageToken {
			return nil, anno, fmt.Errorf("baton-docusign: CLM API returned a non-advancing pagination token while listing groups for member %s", memberID)
		}
		pageToken = nextPageToken
	}

	return nil, anno, fmt.Errorf("baton-docusign: exceeded %d pages while listing groups for member %s", maxMemberGroupPages, memberID)
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

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
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

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}

// GetMemberWorkflowQueues lists the workflow queues a CLM member belongs to. Like
// GetMemberGroups, this fetches the member's complete set rather than exposing a page
// token: clm_workflow_queue's List() (pkg/connector/clm_workflow_queues.go) needs every
// queue a member is in to build its member->queues index, not one page at a time.
// Confirmed read-only intent per the API's documented surface: there is no reverse
// lookup (queue to members) and no membership grant/revoke endpoint, only work-item
// assign/unassign — which this connector doesn't sync (see clm_workflow_queues.go).
func (c *Client) GetMemberWorkflowQueues(ctx context.Context, memberID string) ([]ClmWorkflowQueue, annotations.Annotations, error) {
	// maxMemberQueuePages mirrors GetMemberGroups' identical safety bound — see its
	// comment for the rationale.
	const maxMemberQueuePages = 1000

	var all []ClmWorkflowQueue
	var anno annotations.Annotations
	pageToken := ""

	for i := 0; i < maxMemberQueuePages; i++ {
		page, nextPageToken, pageAnno, err := c.getMemberWorkflowQueuesPage(ctx, memberID, PageOptions{PageToken: pageToken})
		if err != nil {
			return nil, anno, err
		}
		anno = append(anno, pageAnno...)
		all = append(all, page...)

		if nextPageToken == "" {
			return all, anno, nil
		}
		if nextPageToken == pageToken {
			return nil, anno, fmt.Errorf("baton-docusign: CLM API returned a non-advancing pagination token while listing workflow queues for member %s", memberID)
		}
		pageToken = nextPageToken
	}

	return nil, anno, fmt.Errorf("baton-docusign: exceeded %d pages while listing workflow queues for member %s", maxMemberQueuePages, memberID)
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

	nextToken, err := getClmNextToken(requestedPage, len(page.Items), page.Next != "", page.Total)
	if err != nil {
		return nil, "", anno, err
	}
	return page.Items, nextToken, anno, nil
}
