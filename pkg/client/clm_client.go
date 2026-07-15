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
//   - GET  /v2/{accountId}/folders/{id}?expand=Security - Get a folder with its explicit security entries
//   - PATCH /v2/{accountId}/folders/{id} - Update folder security (grant: set an {AccessType,Item} entry;
//     revoke: set that entry's AccessType to "NoAccess")
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
// e.g. api.{site}.{region}.clm.docusign.net) is resolved from an `api_base_url` value
// on the OAuth token, per DocuSign's documentation. See ensureClmInitialized below.
//
// # Pagination
//
// Offset-based: pageSortParams.offset / pageSortParams.limit / pageSortParams.sortProperty /
// pageSortParams.sortDirection / pageSortParams.filter query params; responses wrap
// results as {First, Href, Items, Last, Limit, Next, Offset, Previous, Total}.
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
)

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
)

// ensureClmInitialized resolves the CLM Object API base URL, separately from
// eSignature's ensureInitialized/baseURI. DocuSign's OAuth token exchange carries the
// base URL in an "api_base_url" field, surfaced here via the OAuth2 token's Extra data.
// If it's absent, this returns a clear error rather than guessing at a URL shape, since
// guessing wrong here would silently point every CLM call at a nonexistent host.
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

	tok, err := c.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("baton-docusign: failed to get token for CLM base URL resolution: %w", err)
	}

	apiBaseURL, _ := tok.Extra("api_base_url").(string)
	if apiBaseURL == "" {
		return fmt.Errorf("baton-docusign: could not resolve the DocuSign CLM API base URL " +
			"(expected an \"api_base_url\" field on the OAuth token)")
	}

	c.clmBaseURI = apiBaseURL
	c.clmBaseURIReady = true

	return nil
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
// path segment), in order.
func (c *Client) prepareClmPagedRequest(endpoint string, options PageOptions, extra ...any) (*url.URL, error) {
	c.mutex.RLock()
	clmBaseURI := c.clmBaseURI
	accountId := c.accountId
	c.mutex.RUnlock()

	baseURL, err := url.Parse(clmBaseURI)
	if err != nil {
		return nil, fmt.Errorf("baton-docusign: invalid CLM base URL: %w", err)
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

	searchURL, err := c.prepareClmPagedRequest(clmSearchFolders, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmFolderPage
	anno, err := c.doClmRequest(ctx, http.MethodPost, searchURL, struct{}{}, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to search CLM folders: %w", err)
	}

	return page.Items, getClmNextToken(page.ClmPage), anno, nil
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

// PatchFolderSecurity grants or revokes folder security by setting a single
// {AccessType, Item} entry on the folder. Grant: AccessType is the target tier
// (e.g. "View", "ViewEdit"). Revoke: AccessType is "NoAccess".
func (c *Client) PatchFolderSecurity(ctx context.Context, folderID string, entry ClmSecurityEntry) (annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, err
	}

	folderURL, err := c.buildClmClientURL(clmPatchFolder, folderID)
	if err != nil {
		return nil, err
	}

	body := ClmFolderSecurityPatch{Security: []ClmSecurityEntry{entry}}
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

	listURL, err := c.prepareClmPagedRequest(clmGetGroups, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmGroupPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, listURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list CLM groups: %w", err)
	}

	return page.Items, getClmNextToken(page.ClmPage), anno, nil
}

// GetGroupMembers lists the members of a CLM group.
//
// Pagination: offset/limit, see package doc.
func (c *Client) GetGroupMembers(ctx context.Context, groupID string, options PageOptions) ([]ClmMember, string, annotations.Annotations, error) {
	if err := c.ensureClmReady(ctx); err != nil {
		return nil, "", nil, err
	}

	membersURL, err := c.prepareClmPagedRequest(clmGetGroupMembers, options, groupID)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmMemberPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, membersURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list members of CLM group %s: %w", groupID, err)
	}

	return page.Items, getClmNextToken(page.ClmPage), anno, nil
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

	listURL, err := c.prepareClmPagedRequest(clmGetMembers, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmMemberPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, listURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list CLM members: %w", err)
	}

	return page.Items, getClmNextToken(page.ClmPage), anno, nil
}

// GetMemberGroups gets the FULL current list of groups a member belongs to — required
// before Grant/Revoke, since both are read-modify-write against this list (Patch is
// additive/merge, Put is full-replace). This method pages to completion internally:
// callers need the complete list, not one page of it — Revoke in
// particular does a full-replace Put using this result, so a truncated list here would
// silently drop the member's memberships in every group beyond the first page.
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

	groupsURL, err := c.prepareClmPagedRequest(clmGetMemberGroups, options, memberID)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmGroupPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, groupsURL, nil, &page, uhttp.WithNoCache())
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to get groups for CLM member %s: %w", memberID, err)
	}

	return page.Items, getClmNextToken(page.ClmPage), anno, nil
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

	listURL, err := c.prepareClmPagedRequest(clmGetPermissionSet, options)
	if err != nil {
		return nil, "", nil, err
	}

	var page ClmPermissionSetPage
	anno, err := c.doClmRequest(ctx, http.MethodGet, listURL, nil, &page)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-docusign: failed to list CLM permission sets: %w", err)
	}

	return page.Items, getClmNextToken(page.ClmPage), anno, nil
}
