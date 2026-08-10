package client_test

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
)

func TestSearchFolders_Pagination(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	var all []client.ClmFolder
	pageToken := ""
	for i := 0; i < 10; i++ { // safety bound for the test loop itself
		folders, next, _, err := c.SearchFolders(ctx, client.PageOptions{PageSize: 2, PageToken: pageToken})
		if err != nil {
			t.Fatalf("SearchFolders page %d: %v", i, err)
		}
		all = append(all, folders...)
		if next == "" {
			break
		}
		pageToken = next
	}

	if len(all) != 3 {
		t.Fatalf("expected 3 folders across all pages, got %d", len(all))
	}
	// Search results are summaries — no Security field.
	for _, f := range all {
		if len(f.Security.Groups.Items) != 0 || len(f.Security.Roles.Items) != 0 || len(f.Security.Users.Items) != 0 {
			t.Errorf("folder %s: expected Search to omit Security, got %+v", f.Name, f.Security)
		}
	}
}

func TestGetFolder_ExpandSecurity(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	t.Run("without expand, Security is absent", func(t *testing.T) {
		folder, _, err := c.GetFolder(ctx, "folder-contracts")
		if err != nil {
			t.Fatalf("GetFolder: %v", err)
		}
		if len(folder.Security.Groups.Items) != 0 || len(folder.Security.Roles.Items) != 0 || len(folder.Security.Users.Items) != 0 {
			t.Errorf("expected no Security without ?expand=Security, got %+v", folder.Security)
		}
	})

	t.Run("with expand=Security, entries are populated across all 3 principal types", func(t *testing.T) {
		folder, _, err := c.GetFolder(ctx, "folder-contracts", "Security")
		if err != nil {
			t.Fatalf("GetFolder: %v", err)
		}
		if len(folder.Security.Groups.Items) != 2 {
			t.Fatalf("expected 2 seeded group security entries, got %d: %+v", len(folder.Security.Groups.Items), folder.Security.Groups.Items)
		}
		if len(folder.Security.Roles.Items) != 1 {
			t.Fatalf("expected 1 seeded role security entry, got %d: %+v", len(folder.Security.Roles.Items), folder.Security.Roles.Items)
		}
		if len(folder.Security.Users.Items) != 1 {
			t.Fatalf("expected 1 seeded user security entry, got %d: %+v", len(folder.Security.Users.Items), folder.Security.Users.Items)
		}
	})

	t.Run("unknown folder returns an error", func(t *testing.T) {
		if _, _, err := c.GetFolder(ctx, "folder-does-not-exist"); err == nil {
			t.Error("expected an error for an unknown folder ID, got nil")
		}
	})
}

// TestPatchFolderSecurity_SendsExactEntries confirms PatchFolderSecurity's basic
// plumbing: the write it's given is exactly what a subsequent read reflects.
// Multi-principal preservation (the reason callers must pass the complete Security
// state, not just the one changed entry) is covered by
// clm_folders_test.go's TestClmFolderBuilder_GrantAndRevoke_PreservesOtherPrincipals.
func TestPatchFolderSecurity_SendsExactEntries(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	ctx := context.Background()

	groupHref := srv.GroupHref("group-ops")

	// folder-templates starts with no Security entries.
	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Groups: []client.ClmGroupSecurityEntry{{AccessType: client.ClmAccessTypeView, Href: groupHref}},
	}); err != nil {
		t.Fatalf("PatchFolderSecurity (grant): %v", err)
	}

	sec := srv.FolderSecurity("folder-templates")
	if len(sec.Groups.Items) != 1 || sec.Groups.Items[0].AccessType != client.ClmAccessTypeView || sec.Groups.Items[0].Href != groupHref {
		t.Fatalf("expected one View entry for %s, got %+v", groupHref, sec.Groups.Items)
	}

	// Sending a single-entry Groups list for the same Href again replaces the prior entry.
	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Groups: []client.ClmGroupSecurityEntry{{AccessType: client.ClmAccessTypeNoAccess, Href: groupHref}},
	}); err != nil {
		t.Fatalf("PatchFolderSecurity (revoke): %v", err)
	}

	sec = srv.FolderSecurity("folder-templates")
	if len(sec.Groups.Items) != 1 {
		t.Fatalf("expected the existing entry to be updated in place, not duplicated: %+v", sec.Groups.Items)
	}
	if sec.Groups.Items[0].AccessType != client.ClmAccessTypeNoAccess {
		t.Errorf("expected AccessType NoAccess after revoke, got %q", sec.Groups.Items[0].AccessType)
	}
}

func TestListGroups_Pagination(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	groups, next, _, err := c.ListGroups(ctx, client.PageOptions{PageSize: 2})
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected first page of 2 groups, got %d", len(groups))
	}
	if next == "" {
		t.Fatal("expected a next-page token — there are 5 seeded groups")
	}
}

func TestGetGroupMembers(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	members, _, _, err := c.GetGroupMembers(ctx, "group-legal", client.PageOptions{})
	if err != nil {
		t.Fatalf("GetGroupMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected group-legal to have 2 members (alice, bob), got %d", len(members))
	}
}

func TestGetMemberGroups_PaginatesAcrossPages(t *testing.T) {
	// Regression test: GetMemberGroups previously issued a single unpaginated request
	// and silently truncated any member with more groups than fit on one page — a
	// Critical bug, since Revoke's full-replace Put then wiped every group beyond
	// page 1. GetMemberGroups pages internally at the client's DefaultPageSize (100,
	// not caller-configurable), so member-frank is seeded with 105 groups — the
	// smallest count that forces a real second request rather than fitting on one page.
	srv, c := clmtest.NewServer(t)
	ctx := context.Background()

	groups, _, err := c.GetMemberGroups(ctx, "member-frank")
	if err != nil {
		t.Fatalf("GetMemberGroups: %v", err)
	}
	if len(groups) != 105 {
		t.Fatalf("expected all 105 of member-frank's groups to be returned (paginated internally), got %d", len(groups))
	}
	if got := srv.MemberGroupsRequestCount(); got < 2 {
		t.Fatalf("expected GetMemberGroups to issue at least 2 HTTP requests to page through 105 groups, but the mock server only saw %d — pagination is not actually happening", got)
	}
}

func TestGetMemberGroups_SmallMemberIsSingleRequest(t *testing.T) {
	// Sanity check for the test above: a member with few groups should NOT trigger
	// extra requests, confirming the request-count assertion is meaningful and not
	// just always > 1 regardless of data size.
	srv, c := clmtest.NewServer(t)
	ctx := context.Background()

	if _, _, err := c.GetMemberGroups(ctx, "member-eve"); err != nil {
		t.Fatalf("GetMemberGroups: %v", err)
	}
	if got := srv.MemberGroupsRequestCount(); got != 1 {
		t.Fatalf("expected exactly 1 request for a member with only 5 groups, got %d", got)
	}
}

func TestPatchMemberGroups_IsAdditive(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	ctx := context.Background()

	// member-carol starts in group-finance only.
	before := srv.MemberGroups("member-carol")
	if len(before) != 1 {
		t.Fatalf("precondition failed: expected member-carol to start with 1 group, got %v", before)
	}

	if _, err := c.PatchMemberGroups(ctx, "member-carol", []client.ClmGroup{{Href: srv.GroupHref("group-legal")}}); err != nil {
		t.Fatalf("PatchMemberGroups: %v", err)
	}

	after := srv.MemberGroups("member-carol")
	if len(after) != 2 {
		t.Fatalf("expected member-carol to end up in 2 groups (additive), got %v", after)
	}
}

func TestPutMemberGroups_IsFullReplace(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	ctx := context.Background()

	// member-bob starts in group-legal and group-finance.
	before := srv.MemberGroups("member-bob")
	if len(before) != 2 {
		t.Fatalf("precondition failed: expected member-bob to start with 2 groups, got %v", before)
	}

	// Put with only group-legal — group-finance should be dropped.
	if _, err := c.PutMemberGroups(ctx, "member-bob", []client.ClmGroup{{Href: srv.GroupHref("group-legal")}}); err != nil {
		t.Fatalf("PutMemberGroups: %v", err)
	}

	after := srv.MemberGroups("member-bob")
	if len(after) != 1 || after[0] != "group-legal" {
		t.Fatalf("expected member-bob to end up in only group-legal, got %v", after)
	}

	// The reverse mapping must also reflect the removal — group-finance no longer
	// lists bob as a member.
	financeMembers, _, _, err := c.GetGroupMembers(ctx, "group-finance", client.PageOptions{})
	if err != nil {
		t.Fatalf("GetGroupMembers: %v", err)
	}
	for _, m := range financeMembers {
		if m.Email == "bob@example.com" {
			t.Error("expected bob to be removed from group-finance's member list after PutMemberGroups")
		}
	}
}

func TestListMembers_Pagination(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	var all []client.ClmMember
	pageToken := ""
	for i := 0; i < 10; i++ {
		members, next, _, err := c.ListMembers(ctx, client.PageOptions{PageSize: 2, PageToken: pageToken})
		if err != nil {
			t.Fatalf("ListMembers page %d: %v", i, err)
		}
		all = append(all, members...)
		if next == "" {
			break
		}
		pageToken = next
	}
	if len(all) != 6 { // alice, bob, carol, dave, eve, frank
		t.Fatalf("expected 6 members across all pages, got %d", len(all))
	}
}

func TestListPermissionSets_Pagination(t *testing.T) {
	// The connector-layer bug fixed in this project's second review round was that
	// clmPermissionSetBuilder.List() never threaded this client method's pagination
	// token. This test confirms the underlying client method itself is fully
	// paginated; the connector-layer test confirms the builder consumes it correctly.
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	var all []client.ClmPermissionSet
	pageToken := ""
	for i := 0; i < 10; i++ {
		sets, next, _, err := c.ListPermissionSets(ctx, client.PageOptions{PageSize: 2, PageToken: pageToken})
		if err != nil {
			t.Fatalf("ListPermissionSets page %d: %v", i, err)
		}
		all = append(all, sets...)
		if next == "" {
			break
		}
		pageToken = next
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 permission sets across all pages, got %d", len(all))
	}
}

func TestGetMemberWorkflowQueues(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	t.Run("member in two queues", func(t *testing.T) {
		queues, _, err := c.GetMemberWorkflowQueues(ctx, "member-bob")
		if err != nil {
			t.Fatalf("GetMemberWorkflowQueues: %v", err)
		}
		if len(queues) != 2 {
			t.Fatalf("expected member-bob to be in 2 workflow queues, got %d: %+v", len(queues), queues)
		}
	})

	t.Run("member in no queues", func(t *testing.T) {
		queues, _, err := c.GetMemberWorkflowQueues(ctx, "member-carol")
		if err != nil {
			t.Fatalf("GetMemberWorkflowQueues: %v", err)
		}
		if len(queues) != 0 {
			t.Fatalf("expected member-carol to be in 0 workflow queues, got %d: %+v", len(queues), queues)
		}
	})

	t.Run("unknown member returns an error", func(t *testing.T) {
		if _, _, err := c.GetMemberWorkflowQueues(ctx, "member-does-not-exist"); err == nil {
			t.Error("expected an error for an unknown member ID, got nil")
		}
	})
}
