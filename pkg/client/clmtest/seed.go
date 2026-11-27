package clmtest

import (
	"fmt"
	"path"

	"github.com/conductorone/baton-docusign/pkg/client"
)

const (
	groupTypeSecurity      = "security"
	groupLegalID           = "group-legal"
	groupFinanceID         = "group-finance"
	memberBobID            = "member-bob"
	memberEveID            = "member-eve"
	roleFullSubscriberName = "FullSubscriber"
	roleUnheldName         = "UnheldRole"
	rootFolderID           = "folder-root"
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
		{Email: "clm.admin@example.com", UserName: "clm.admin", FirstName: "Clm", LastName: "Admin", Role: "CLMAdmin"},
		{Email: "clm.user@example.com", UserName: "clm.user", FirstName: "Clm", LastName: "User", Role: "CLMUser"},
		{Email: "norole@example.com", UserName: "norole", FirstName: "No", LastName: "Role"},                                         // empty Role
		{Email: "reviewer@example.com", UserName: "reviewer", FirstName: "Contract", LastName: "Reviewer", Role: "ContractReviewer"}, // role outside client.ClmRoles
	}
	memberIDs := []string{"member-alice", memberBobID, "member-carol", "member-dave", memberEveID, "member-clm-admin", "member-clm-user", "member-no-role", "member-contract-reviewer"}
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

	s.addFolder(rootFolderID, "Root", "")
	s.addFolder("folder-templates", "Templates", rootFolderID)

	contractsFolder := s.addFolder("folder-contracts", "Contracts", rootFolderID)
	contractsFolder.Security = client.ClmFolderSecurity{
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
			// Roles held by members but outside client.ClmRoles — synced from the members list.
			{AccessType: client.ClmAccessTypeViewEdit, Item: "CLMAdmin"},
			{AccessType: client.ClmAccessTypeView, Item: "CLMUser"},
			{AccessType: client.ClmAccessTypeView, Item: "ContractReviewer"},
			// In client.ClmRoles, so synced even though no member holds it.
			{AccessType: client.ClmAccessTypeView, Item: "UserAdministrator"},
			// Neither shipped nor held by any member — not synced as clm_role, so skipped.
			{AccessType: client.ClmAccessTypeView, Item: roleUnheldName},
		},
		Users: []client.ClmUserSecurityEntry{
			// Known tier granted to a member — tests clm_member routing.
			{AccessType: client.ClmAccessTypeView, Href: s.MemberHref(memberBobID)},
		},
	}

	// Workflow queues: no list-all endpoint exists for this object (see
	// pkg/connector/clm_workflow_queues.go), so they're only discoverable by scanning
	// members — seeded here purely via memberWorkflowQueues, never via a queueOrder
	// slice. member-bob is in both (tests dedup across members when building the
	// distinct-queue set); member-alice is in only one; carol/dave/eve/frank are in
	// none (tests that a member scan handles "no queues" without emitting anything for
	// them, and that clm_workflow_queue's List() doesn't invent queues from members
	// that have none).
	queueOnboardingID := "queue-onboarding"
	queueEscalationsID := "queue-escalations"
	s.workflowQueues[queueOnboardingID] = &client.ClmWorkflowQueue{
		Name: "Onboarding",
		Href: s.WorkflowQueueHref(queueOnboardingID),
	}
	s.workflowQueues[queueEscalationsID] = &client.ClmWorkflowQueue{
		Name: "Escalations",
		Href: s.WorkflowQueueHref(queueEscalationsID),
	}
	s.memberWorkflowQueues["member-alice"] = []string{queueOnboardingID}
	s.memberWorkflowQueues[memberBobID] = []string{queueOnboardingID, queueEscalationsID}
}

// addFolder registers a folder under parentID ("" for the root) and returns it.
func (s *Server) addFolder(id, name, parentID string) *client.ClmFolder {
	folder := &client.ClmFolder{Name: name, Path: "/", Href: s.FolderHref(id)}
	if parentID != "" {
		parent := s.folders[parentID]
		folder.Path = path.Join(parent.Path, name)
		folder.ParentFolder = &client.ClmFolderRef{Href: parent.Href, Name: parent.Name}
		s.folderChildren[parentID] = append(s.folderChildren[parentID], id)
	}
	s.folders[id] = folder
	return folder
}

// addChildFolders adds count numbered folders under parentID and returns their IDs.
func (s *Server) addChildFolders(parentID, namePrefix string, count int) []string {
	ids := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("%s-%03d", parentID, i)
		s.addFolder(id, fmt.Sprintf("%s %03d", namePrefix, i), parentID)
		ids = append(ids, id)
	}
	return ids
}

// SeedFolderTree adds a deep, wide folder tree under the root, including one folder with two
// pages of children. Kept out of the default seed so other tests' folder counts don't change.
func (s *Server) SeedFolderTree() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.addFolder("folder-other-sources", "Other Sources", rootFolderID)
	s.addFolder("folder-salesforce", "Salesforce", "folder-other-sources")
	s.addFolder("folder-agreements", "Agreements", rootFolderID)
	s.addFolder("folder-company", "Company", rootFolderID)

	s.addFolder("folder-trash", "Trash", rootFolderID)
	s.addFolder("folder-trash-20261103", "20261103", "folder-trash")
	s.addFolder("folder-trash-old-contracts", "Old Contracts", "folder-trash-20261103")

	agreementIDs := s.addChildFolders("folder-agreements", "Agreement", 150)
	for _, agreementID := range agreementIDs[:10] {
		s.addChildFolders(agreementID, "Addendum", 5)
	}

	for _, salesforceAccountID := range s.addChildFolders("folder-salesforce", "Account", 20) {
		s.addChildFolders(salesforceAccountID, "Opportunity", 2)
	}

	for i, departmentID := range s.addChildFolders("folder-company", "Department", 10) {
		teamIDs := s.addChildFolders(departmentID, "Team", 5)
		if i < 5 {
			s.addChildFolders(teamIDs[0], "Project", 4)
		}
	}
}
