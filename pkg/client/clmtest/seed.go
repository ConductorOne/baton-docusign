package clmtest

import (
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
)

const (
	groupTypeSecurity      = "security"
	groupLegalID           = "group-legal"
	groupFinanceID         = "group-finance"
	memberBobID            = "member-bob"
	memberEveID            = "member-eve"
	roleFullSubscriberName = "FullSubscriber"
)

// seed populates a freshly constructed Server with fixture data. Diversity matters
// more than count here — see the anti-drift seed-scope guidance this mirrors:
//   - a member with no group memberships (member-dave) — tests the empty-grants path
//   - a member with MORE groups than one page (member-eve, 5 groups) — tests
//     GetMemberGroups' pagination-to-completion loop, the subject of a fixed bug
//   - overlapping group membership (member-bob is in two groups)
//   - a folder with an AccessType-based Security entry, a flags-based one, a
//     Role-granted one, and one that matches no known tier (tests the Grants()
//     skip-rather-than-guess path)
//   - 5 permission sets with a small page size in tests — tests
//     clmPermissionSetBuilder.List()'s pagination, the subject of another fixed bug
func seed(s *Server) {
	groups := []*client.ClmGroup{
		{Name: "Legal", GroupType: groupTypeSecurity},
		{Name: "Finance", GroupType: groupTypeSecurity},
		{Name: "Operations", GroupType: groupTypeSecurity},
		{Name: "HR", GroupType: "distribution"},
		{Name: "IT", GroupType: groupTypeSecurity},
	}
	groupIDs := []string{groupLegalID, groupFinanceID, "group-ops", "group-hr", "group-it"}
	for i, g := range groups {
		id := groupIDs[i]
		g.Href = s.GroupHref(id)
		s.groups[id] = g
		s.groupOrder = append(s.groupOrder, id)
	}

	members := []*client.ClmMember{
		{Email: "alice@example.com", UserName: "alice", FirstName: "Alice", LastName: "Alpha", Role: roleFullSubscriberName},
		{Email: "bob@example.com", UserName: "bob", FirstName: "Bob", LastName: "Beta", Role: roleFullSubscriberName},
		{Email: "carol@example.com", UserName: "carol", FirstName: "Carol", LastName: "Gamma", Role: "LimitedSubscriber"},
		{Email: "dave@example.com", UserName: "dave", FirstName: "Dave", LastName: "Delta", Role: "Guest"}, // no groups
		{Email: "eve@example.com", UserName: "eve", FirstName: "Eve", LastName: "Epsilon", Role: "SuperAdministrator"},
	}
	memberIDs := []string{"member-alice", memberBobID, "member-carol", "member-dave", memberEveID}
	for i, m := range members {
		id := memberIDs[i]
		m.Href = s.MemberHref(id)
		s.members[id] = m
		s.memberOrder = append(s.memberOrder, id)
	}

	// member-frank belongs to 105 groups — more than GetMemberGroups' default page
	// size (100) — so tests can regression-check that it pages to completion rather
	// than silently returning a single truncated page (the subject of a fixed Critical
	// bug). The 105 groups are synthetic and kept separate from the 5 named groups
	// above so those stay easy to read in other tests.
	const bulkGroupCount = 105
	frank := &client.ClmMember{Email: "frank@example.com", UserName: "frank", FirstName: "Frank", LastName: "Foxtrot", Role: roleFullSubscriberName}
	frank.Href = s.MemberHref("member-frank")
	s.members["member-frank"] = frank
	s.memberOrder = append(s.memberOrder, "member-frank")

	bulkGroupIDs := make([]string, 0, bulkGroupCount)
	for i := 1; i <= bulkGroupCount; i++ {
		gid := fmt.Sprintf("group-bulk-%03d", i)
		g := &client.ClmGroup{Name: fmt.Sprintf("Bulk Group %03d", i), GroupType: groupTypeSecurity}
		g.Href = s.GroupHref(gid)
		s.groups[gid] = g
		s.groupOrder = append(s.groupOrder, gid)
		s.groupMembers[gid] = []string{"member-frank"}
		bulkGroupIDs = append(bulkGroupIDs, gid)
	}
	s.memberGroups["member-frank"] = bulkGroupIDs

	// Membership, seeded consistently from both directions.
	s.memberGroups["member-alice"] = []string{groupLegalID}
	s.memberGroups[memberBobID] = []string{groupLegalID, groupFinanceID} // overlap
	s.memberGroups["member-carol"] = []string{groupFinanceID}
	s.memberGroups["member-dave"] = []string{} // no groups
	s.memberGroups[memberEveID] = []string{groupLegalID, groupFinanceID, "group-ops", "group-hr", "group-it"}

	s.groupMembers[groupLegalID] = []string{"member-alice", memberBobID}
	s.groupMembers[groupFinanceID] = []string{memberBobID, "member-carol"}
	s.groupMembers["group-ops"] = []string{memberEveID}
	s.groupMembers["group-hr"] = []string{memberEveID}
	s.groupMembers["group-it"] = []string{memberEveID}

	permissionSets := []*client.ClmPermissionSet{
		{Name: "Administrator", Permissions: []string{"CanManageUsers", "CanManageWorkflows"}},
		{Name: "Editor", Permissions: []string{"CanEditDocuments"}},
		{Name: "Viewer", Permissions: []string{"CanViewDocuments"}},
		{Name: "Reports", Permissions: []string{"CanRunReports"}},
		{Name: "Integration", Permissions: []string{"CanUseAPI"}},
	}
	permissionSetIDs := []string{"ps-admin", "ps-editor", "ps-viewer", "ps-reports", "ps-integration"}
	for i, ps := range permissionSets {
		id := permissionSetIDs[i]
		ps.Href = s.FolderHref(id) // any Href-shaped URI works; only the trailing ID is used
		s.permissionSets[id] = ps
		s.permissionSetOrder = append(s.permissionSetOrder, id)
	}

	rootFolder := &client.ClmFolder{Name: "Root", Path: "/"}
	rootFolder.Href = s.FolderHref("folder-root")
	s.folders["folder-root"] = rootFolder
	s.folderOrder = append(s.folderOrder, "folder-root")

	templatesFolder := &client.ClmFolder{Name: "Templates", Path: "/Templates"}
	templatesFolder.Href = s.FolderHref("folder-templates")
	s.folders["folder-templates"] = templatesFolder
	s.folderOrder = append(s.folderOrder, "folder-templates")

	contractsFolder := &client.ClmFolder{
		Name: "Contracts",
		Path: "/Contracts",
		Security: client.ClmFolderSecurity{
			Groups: []client.ClmGroupSecurityEntry{
				// Known tier granted to a group — tests slug-from-AccessType and
				// group-Href routing (should carry GrantExpandable at the connector
				// layer).
				{AccessType: client.ClmAccessTypeViewEdit, Href: s.GroupHref(groupLegalID)},
				// "Custom" — not one of the 5 grantable tiers — tests that Grants()
				// skips rather than guesses.
				{AccessType: client.ClmAccessTypeCustom, Href: s.GroupHref("group-finance")},
			},
			Roles: []client.ClmRoleSecurityEntry{
				// Role-granted entry — tests clm_role routing.
				{AccessType: client.ClmAccessTypeView, Item: roleFullSubscriberName},
			},
			Users: []client.ClmUserSecurityEntry{
				// Known tier granted to a member — tests clm_member routing.
				{AccessType: client.ClmAccessTypeView, Href: s.MemberHref(memberBobID)},
			},
		},
	}
	contractsFolder.Href = s.FolderHref("folder-contracts")
	s.folders["folder-contracts"] = contractsFolder
	s.folderOrder = append(s.folderOrder, "folder-contracts")
}
