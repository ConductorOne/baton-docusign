package clmtest

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/conductorone/baton-docusign/pkg/client"
)

// idFromHref extracts the trailing path segment of a Href — see client.IDFromHref's doc.
func idFromHref(href string) string {
	return client.IDFromHref(href)
}

// Doc URL: https://developers.docusign.com/docs/clm-api/reference/objects/folders/ (Search).
func (s *Server) handleSearchFolders(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	page, meta := pageSlice(r, s.folderOrder)
	items := make([]client.ClmFolder, 0, len(page))
	for _, id := range page {
		f := *s.folders[id]
		f.Security = client.ClmFolderSecurity{} // Search results are summaries; Security only comes via ?expand on Get
		items = append(items, f)
	}
	writeJSON(w, client.ClmFolderPage{ClmPage: meta, Items: items})
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
func (s *Server) handlePatchFolder(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := r.PathValue("id")
	f, ok := s.folders[id]
	if !ok {
		writeNotFound(w)
		return
	}

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
		Security struct {
			Groups []map[string]any `json:"Groups"`
			Roles  []map[string]any `json:"Roles"`
			Users  []map[string]any `json:"Users"`
		} `json:"Security"`
	}
	if json.Unmarshal(bodyBytes, &rawBody) == nil {
		for _, entries := range [][]map[string]any{rawBody.Security.Groups, rawBody.Security.Roles, rawBody.Security.Users} {
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

	var body client.ClmFolderSecurityPatch
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.Security = client.ClmFolderSecurity{
		Groups: client.ClmGroupSecurityPage{Items: body.Security.Groups},
		Roles:  client.ClmRoleSecurityPage{Items: body.Security.Roles},
		Users:  client.ClmUserSecurityPage{Items: body.Security.Users},
	}

	writeJSON(w, *f)
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
