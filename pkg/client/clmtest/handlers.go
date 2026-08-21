package clmtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/conductorone/baton-docusign/pkg/client"
)

// idFromHref extracts the trailing path segment of a Href — see client.IDFromHref's doc.
func idFromHref(href string) string {
	return client.IDFromHref(href)
}

// folderSearchResults builds the flat, paginated ClmFolderPage for a folder search —
// shared by the create/poll (wrapped in {Status, Href, Result}) and continuation
// (bare) responses. Search results are summaries; Security only comes via ?expand on
// Get, mirroring the real API.
func (s *Server) folderSearchResults(r *http.Request) client.ClmFolderPage {
	page, meta := pageSlice(r, s.folderOrder)
	items := make([]client.ClmFolder, 0, len(page))
	for _, id := range page {
		f := *s.folders[id]
		f.Security = client.ClmFolderSecurity{}
		items = append(items, f)
	}
	return client.ClmFolderPage{ClmPage: meta, Items: items}
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/tasks/foldersearchtasks/
// (Post). Real CLM requires a recognized search parameter in the body (confirmed live:
// {"Title": ""} matches every folder) — this mock doesn't replicate that validation,
// since every real client call already sends the confirmed-working body.
func (s *Server) handleCreateFolderSearchTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextFolderSearchTaskID++
	taskID := strconv.Itoa(s.nextFolderSearchTaskID)
	taskHref := fmt.Sprintf("%s/v2/%s/foldersearchtasks/%s", s.baseURL, AccountID, taskID)

	status := "Success"
	if s.pendingFolderSearchPolls > 0 {
		status = "Processing"
	}
	resp := client.ClmFolderSearchTaskResponse{Status: status, Href: taskHref}
	if status == "Success" {
		result := s.folderSearchResults(r)
		if s.omitFolderSearchResultHref {
			// Leave Href empty and force a "more pages remain" shape so SearchFolders'
			// empty-Href continuation guard is reachable (the create POST has no
			// offset/limit query, so folderSearchResults alone would return everything).
			if len(result.Items) > 1 {
				result.Items = result.Items[:1]
			}
			result.Limit = 1
			result.Total = len(s.folderOrder)
			result.Next = "more"
			result.Offset = 0
		} else {
			result.Href = taskHref + "/result"
		}
		resp.Result = &result
	}
	writeJSON(w, resp)
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/tasks/foldersearchtasks/get/
func (s *Server) handlePollFolderSearchTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	taskID := r.PathValue("id")
	taskHref := fmt.Sprintf("%s/v2/%s/foldersearchtasks/%s", s.baseURL, AccountID, taskID)

	if s.pendingFolderSearchPolls > 0 {
		s.pendingFolderSearchPolls--
		writeJSON(w, client.ClmFolderSearchTaskResponse{Status: "Processing", Href: taskHref})
		return
	}

	result := s.folderSearchResults(r)
	if !s.omitFolderSearchResultHref {
		result.Href = taskHref + "/result"
	}
	writeJSON(w, client.ClmFolderSearchTaskResponse{Status: "Success", Href: taskHref, Result: &result})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/tasks/foldersearchtasks/getsearchresult/
func (s *Server) handleFolderSearchTaskResult(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	writeJSON(w, s.folderSearchResults(r))
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/folders/get/
func (s *Server) handleGetFolder(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := r.PathValue("id")
	f, ok := s.folders[id]
	if !ok {
		writeNotFound(w)
		return
	}

	out := *f
	if r.URL.Query().Get("expand") != "Security" {
		out.Security = client.ClmFolderSecurity{}
	}
	writeJSON(w, out)
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/folders/patch/
//
// Replaces the folder's entire Security list with whatever body.Security contains,
// rather than merging/upserting into the existing list. The real API's merge-vs-
// replace semantics here are genuinely undocumented either way, so this picks the
// stricter of the two equally-plausible interpretations deliberately: the connector
// (clm_folders.go's Grant/Revoke) always sends the complete list back, specifically
// because it's safe under replace semantics too. Modeling this endpoint as a merge
// would hide a regression to sending just the one changed entry — replace surfaces it
// immediately as other principals' entries disappearing.
// Doc URL: https://developers.docusign.com/docs/clm-api/reference/tasks/changesecuritytasks/post/
// Simulates PatchFolderSecurity's real endpoint (ChangeSecurityTasks), not the generic
// Folders Patch this replaced — see clm_client.go's PatchFolderSecurity doc for why.
func (s *Server) handleCreateChangeSecurityTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Reject an explicit empty-string AccessType before the normal decode: Go's
	// encoding/json can't otherwise distinguish "AccessType key present but empty"
	// from "key absent," and every *SecurityEntry's AccessType omitempty tag means a
	// real client should never produce the former — decoding into a raw
	// representation here is the only way this mock can actually catch a regression
	// to sending it, across all three of Groups/Roles/Users.
	var rawBody struct {
		Folder struct {
			Security struct {
				Groups []map[string]any `json:"Groups"`
				Roles  []map[string]any `json:"Roles"`
				Users  []map[string]any `json:"Users"`
			} `json:"Security"`
		} `json:"Folder"`
	}
	if json.Unmarshal(bodyBytes, &rawBody) == nil {
		for _, entries := range [][]map[string]any{rawBody.Folder.Security.Groups, rawBody.Folder.Security.Roles, rawBody.Folder.Security.Users} {
			for _, entry := range entries {
				if v, present := entry["AccessType"]; present {
					if str, ok := v.(string); ok && str == "" {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(client.ClmErrorResponse{})
						return
					}
				}
			}
		}
	}

	var body client.ClmChangeSecurityTaskRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	folderID := idFromHref(body.Folder.Href)
	f, ok := s.folders[folderID]
	if !ok {
		writeNotFound(w)
		return
	}

	f.Security = client.ClmFolderSecurity{
		Groups: body.Folder.Security.Groups,
		Roles:  body.Folder.Security.Roles,
		Users:  body.Folder.Security.Users,
	}

	s.nextChangeSecurityTaskID++
	taskHref := fmt.Sprintf("%s/v2/%s/changesecuritytasks/%d", s.baseURL, AccountID, s.nextChangeSecurityTaskID)
	status := client.ClmChangeSecurityStatusSuccess
	if s.pendingChangeSecurityPolls > 0 {
		status = client.ClmChangeSecurityStatusWaiting
	}
	writeJSON(w, client.ClmChangeSecurityTaskResponse{Href: taskHref, Status: status})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/tasks/changesecuritytasks/get/
func (s *Server) handlePollChangeSecurityTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	taskID := r.PathValue("id")
	taskHref := fmt.Sprintf("%s/v2/%s/changesecuritytasks/%s", s.baseURL, AccountID, taskID)

	if s.pendingChangeSecurityPolls > 0 {
		s.pendingChangeSecurityPolls--
		writeJSON(w, client.ClmChangeSecurityTaskResponse{Href: taskHref, Status: client.ClmChangeSecurityStatusWaiting})
		return
	}

	writeJSON(w, client.ClmChangeSecurityTaskResponse{Href: taskHref, Status: client.ClmChangeSecurityStatusSuccess})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/groups/
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	page, meta := pageSlice(r, s.groupOrder)
	items := make([]client.ClmGroup, 0, len(page))
	for _, id := range page {
		items = append(items, *s.groups[id])
	}
	writeJSON(w, client.ClmGroupPage{ClmPage: meta, Items: items})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/groups/getusers/
func (s *Server) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := r.PathValue("id")
	if _, ok := s.groups[id]; !ok {
		writeNotFound(w)
		return
	}

	memberIDs := s.groupMembers[id]
	page, meta := pageSlice(r, memberIDs)
	items := make([]client.ClmMember, 0, len(page))
	for _, mid := range page {
		items = append(items, *s.members[mid])
	}
	writeJSON(w, client.ClmMemberPage{ClmPage: meta, Items: items})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/members/
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	page, meta := pageSlice(r, s.memberOrder)
	items := make([]client.ClmMember, 0, len(page))
	for _, id := range page {
		items = append(items, *s.members[id])
	}
	writeJSON(w, client.ClmMemberPage{ClmPage: meta, Items: items})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/members/ (GetGroups).
func (s *Server) handleMemberGroups(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memberGroupsRequests++

	id := r.PathValue("id")
	if _, ok := s.members[id]; !ok {
		writeNotFound(w)
		return
	}

	groupIDs := s.memberGroups[id]
	page, meta := pageSlice(r, groupIDs)
	items := make([]client.ClmGroup, 0, len(page))
	for _, gid := range page {
		items = append(items, *s.groups[gid])
	}
	writeJSON(w, client.ClmGroupPage{ClmPage: meta, Items: items})
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/members/patch/
// Additive/merge: adds any group in the request the member isn't already in.
func (s *Server) handlePatchMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := r.PathValue("id")
	m, ok := s.members[id]
	if !ok {
		writeNotFound(w)
		return
	}

	var body client.ClmMemberGroupsPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	hrefs := make([]string, 0, len(body.Groups.Items))
	for _, g := range body.Groups.Items {
		hrefs = append(hrefs, g.Href)
		gid := idFromHref(g.Href)
		if !containsString(s.memberGroups[id], gid) {
			s.memberGroups[id] = append(s.memberGroups[id], gid)
			s.groupMembers[gid] = append(s.groupMembers[gid], id)
		}
	}
	s.lastPatchedMemberGroupHrefs[id] = hrefs

	writeJSON(w, *m)
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/members/put/
// Full-replace: the member ends up in exactly the groups listed in the request.
func (s *Server) handlePutMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := r.PathValue("id")
	m, ok := s.members[id]
	if !ok {
		writeNotFound(w)
		return
	}

	var body client.ClmMemberGroupsPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	newGroupIDs := make([]string, 0, len(body.Groups.Items))
	for _, g := range body.Groups.Items {
		newGroupIDs = append(newGroupIDs, idFromHref(g.Href))
	}

	// Remove this member from groups it's leaving.
	for _, oldGid := range s.memberGroups[id] {
		if !containsString(newGroupIDs, oldGid) {
			s.groupMembers[oldGid] = removeString(s.groupMembers[oldGid], id)
		}
	}
	// Add this member to groups it's newly in.
	for _, newGid := range newGroupIDs {
		if !containsString(s.groupMembers[newGid], id) {
			s.groupMembers[newGid] = append(s.groupMembers[newGid], id)
		}
	}

	s.memberGroups[id] = newGroupIDs

	writeJSON(w, *m)
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/permissionsets/
func (s *Server) handleListPermissionSets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	page, meta := pageSlice(r, s.permissionSetOrder)
	items := make([]client.ClmPermissionSet, 0, len(page))
	for _, id := range page {
		items = append(items, *s.permissionSets[id])
	}
	writeJSON(w, client.ClmPermissionSetPage{ClmPage: meta, Items: items})
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}
