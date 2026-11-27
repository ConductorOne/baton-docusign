package client_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPatchFolderSecurity_PollsUntilSuccess is a regression test for
// PatchFolderSecurity's awaitClmChangeSecurityTask branch — unverified against a live
// tenant (see PatchFolderSecurity's doc in clm_client.go), so this mock-driven test is
// this branch's only coverage.
func TestPatchFolderSecurity_PollsUntilSuccess(t *testing.T) {
	original := client.ClmChangeSecurityTaskPollInterval
	client.ClmChangeSecurityTaskPollInterval = time.Millisecond
	defer func() { client.ClmChangeSecurityTaskPollInterval = original }()

	srv, c := clmtest.NewServer(t)
	ctx := context.Background()

	groupHref := srv.GroupHref("group-ops")
	srv.SetPendingChangeSecurityPolls(2)

	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Groups: []client.ClmGroupSecurityEntry{{AccessType: client.ClmAccessTypeView, Href: groupHref}},
	}); err != nil {
		t.Fatalf("PatchFolderSecurity: %v", err)
	}

	sec := srv.FolderSecurity("folder-templates")
	if len(sec.Groups) != 1 || sec.Groups[0].AccessType != client.ClmAccessTypeView || sec.Groups[0].Href != groupHref {
		t.Fatalf("expected one View entry for %s once the task resolves, got %+v", groupHref, sec.Groups)
	}
}

// TestPatchFolderSecurity_RejectsTaskHrefOnUnexpectedHost is a regression test for
// Client.validateClmURL: doClmRequest attaches the bearer token to whatever URL it's
// given, so a task Href pointing at a host other than the discovered CLM base URL must
// be rejected before it's ever dispatched, not just parsed.
func TestPatchFolderSecurity_RejectsTaskHrefOnUnexpectedHost(t *testing.T) {
	original := client.ClmChangeSecurityTaskPollInterval
	client.ClmChangeSecurityTaskPollInterval = time.Millisecond
	defer func() { client.ClmChangeSecurityTaskPollInterval = original }()

	srv, clmClient := clmtest.NewServer(t)
	ctx := context.Background()

	srv.SetPendingChangeSecurityPolls(1)
	srv.SetChangeSecurityTaskHrefOverride("http://attacker.example.com/v2/acct-clm-test/changesecuritytasks/1")

	_, err := clmClient.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Groups: []client.ClmGroupSecurityEntry{{AccessType: client.ClmAccessTypeView, Href: srv.GroupHref("group-ops")}},
	})
	if err == nil {
		t.Fatal("expected PatchFolderSecurity to reject a task href on an unexpected host, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to send CLM credentials") {
		t.Fatalf("expected a host-validation error, got: %v", err)
	}
}

// The mock answers GET .../folders/type with 404 unless systemFolder=root is sent, so a
// successful call pins the query param.
func TestGetRootFolder(t *testing.T) {
	_, clmClient := clmtest.NewServer(t)

	root, _, err := clmClient.GetRootFolder(context.Background())
	if err != nil {
		t.Fatalf("GetRootFolder: %v", err)
	}
	if got := client.IDFromHref(root.Href); got != "folder-root" {
		t.Errorf("expected the root folder folder-root, got %q", got)
	}
	if root.ParentFolder != nil {
		t.Errorf("expected the root folder to have no parent, got %+v", root.ParentFolder)
	}
}

func TestGetRootFolder_NotFoundIsPermissionDenied(t *testing.T) {
	srv, clmClient := clmtest.NewServer(t)
	srv.ForceRootFolderStatus(http.StatusNotFound)

	_, _, err := clmClient.GetRootFolder(context.Background())
	if err == nil {
		t.Fatal("expected an error for a 404 on the root folder, got nil")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s (err: %v)", status.Code(err), err)
	}
}

// Page sizes and the folder order pin pageSortParams.limit and pageSortParams.offset
// reaching the server: a wrong limit changes the sizes, a wrong offset repeats or skips folders.
func TestListChildFolders_Pagination(t *testing.T) {
	tests := []struct {
		name          string
		pageSize      int
		wantPageSizes []int
	}{
		{"explicit page size", 60, []int{60, 60, 30}},
		{"default page size", 0, []int{100, 50}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			srv, clmClient := clmtest.NewServer(t)
			srv.SeedFolderTree() // folder-agreements has 150 direct children
			ctx := context.Background()

			var pageSizes []int
			var gotIDs []string
			pageToken := ""
			for i := 0; i < 10; i++ { // safety bound for the test loop itself
				folders, next, _, err := clmClient.ListChildFolders(ctx, "folder-agreements", client.PageOptions{PageSize: testCase.pageSize, PageToken: pageToken})
				if err != nil {
					t.Fatalf("ListChildFolders page %d: %v", i, err)
				}
				pageSizes = append(pageSizes, len(folders))
				for _, folder := range folders {
					gotIDs = append(gotIDs, client.IDFromHref(folder.Href))
				}
				if next == "" {
					break
				}
				pageToken = next
			}

			if !slices.Equal(pageSizes, testCase.wantPageSizes) {
				t.Fatalf("expected page sizes %v, got %v", testCase.wantPageSizes, pageSizes)
			}
			for i, folderID := range gotIDs {
				if want := fmt.Sprintf("folder-agreements-%03d", i+1); folderID != want {
					t.Fatalf("folder %d: expected %s, got %s", i, want, folderID)
				}
			}
		})
	}
}

func TestListChildFolders_LeafHasNoChildren(t *testing.T) {
	_, clmClient := clmtest.NewServer(t)

	folders, next, _, err := clmClient.ListChildFolders(context.Background(), "folder-templates", client.PageOptions{})
	if err != nil {
		t.Fatalf("ListChildFolders: %v", err)
	}
	if len(folders) != 0 || next != "" {
		t.Errorf("expected no child folders and no next token, got %d folders (next %q)", len(folders), next)
	}
}

func TestListChildFolders_NotFoundIsPermissionDenied(t *testing.T) {
	_, clmClient := clmtest.NewServer(t)

	_, _, _, err := clmClient.ListChildFolders(context.Background(), "folder-missing", client.PageOptions{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected a PermissionDenied error, got %v", err)
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
		if len(folder.Security.Groups) != 0 || len(folder.Security.Roles) != 0 || len(folder.Security.Users) != 0 {
			t.Errorf("expected no Security without ?expand=Security, got %+v", folder.Security)
		}
	})

	t.Run("with expand=Security, entries are populated across all 3 principal types", func(t *testing.T) {
		folder, _, err := c.GetFolder(ctx, "folder-contracts", "Security")
		if err != nil {
			t.Fatalf("GetFolder: %v", err)
		}
		if len(folder.Security.Groups) != 2 {
			t.Fatalf("expected 2 seeded group security entries, got %d: %+v", len(folder.Security.Groups), folder.Security.Groups)
		}
		if len(folder.Security.Roles) != 6 {
			t.Fatalf("expected 6 seeded role security entries, got %d: %+v", len(folder.Security.Roles), folder.Security.Roles)
		}
		if len(folder.Security.Users) != 1 {
			t.Fatalf("expected 1 seeded user security entry, got %d: %+v", len(folder.Security.Users), folder.Security.Users)
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
	if len(sec.Groups) != 1 || sec.Groups[0].AccessType != client.ClmAccessTypeView || sec.Groups[0].Href != groupHref {
		t.Fatalf("expected one View entry for %s, got %+v", groupHref, sec.Groups)
	}

	// Sending a single-entry Groups list for the same Href again replaces the prior entry.
	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Groups: []client.ClmGroupSecurityEntry{{AccessType: client.ClmAccessTypeNoAccess, Href: groupHref}},
	}); err != nil {
		t.Fatalf("PatchFolderSecurity (revoke): %v", err)
	}

	sec = srv.FolderSecurity("folder-templates")
	if len(sec.Groups) != 1 {
		t.Fatalf("expected the existing entry to be updated in place, not duplicated: %+v", sec.Groups)
	}
	if sec.Groups[0].AccessType != client.ClmAccessTypeNoAccess {
		t.Errorf("expected AccessType NoAccess after revoke, got %q", sec.Groups[0].AccessType)
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
	if len(all) != 10 { // alice, bob, carol, dave, eve, clm.admin, clm.user, no-role, contract-reviewer, frank
		t.Fatalf("expected 10 members across all pages, got %d", len(all))
	}
}

func TestListMembers_NotFoundIsPermissionDenied(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceListMembersStatus(http.StatusNotFound)

	_, _, _, err := c.ListMembers(context.Background(), client.PageOptions{})
	if err == nil {
		t.Fatal("expected an error for a 404 on the members list, got nil")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s (err: %v)", status.Code(err), err)
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

	t.Run("member in no queues returns CLM's 404 + ErrorCode 104", func(t *testing.T) {
		// The client surfaces this as an error; treating it as zero queues is the
		// connector layer's call (see clmMemberBuilder.Grants).
		_, _, err := c.GetMemberWorkflowQueues(ctx, "member-carol")
		if !client.IsClmNoWorkflowTaskGroupsError(err) {
			t.Fatalf("expected a no-workflow-task-groups error for member-carol, got: %v", err)
		}
	})

	t.Run("unknown member returns an error", func(t *testing.T) {
		if _, _, err := c.GetMemberWorkflowQueues(ctx, "member-does-not-exist"); err == nil {
			t.Error("expected an error for an unknown member ID, got nil")
		}
	})
}

// TestGetMemberWorkflowQueues_NotFoundWithoutErrorCode104Propagates pins that
// IsClmNoWorkflowTaskGroupsError matches only CLM's "no workflow task groups" 404
// (ErrorCode 104) — any other 404 on this endpoint is a plain NotFound.
func TestGetMemberWorkflowQueues_NotFoundWithoutErrorCode104Propagates(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", http.StatusNotFound)

	_, _, err := c.GetMemberWorkflowQueues(context.Background(), "member-alice")
	if err == nil {
		t.Fatal("expected an error for a 404 without ErrorCode 104, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %s (err: %v)", status.Code(err), err)
	}
	if client.IsClmNoWorkflowTaskGroupsError(err) {
		t.Errorf("expected a 404 without ErrorCode 104 not to match IsClmNoWorkflowTaskGroupsError, got: %v", err)
	}
}

func TestGetMemberWorkflowQueues_PaginatesAcrossPages(t *testing.T) {
	// Regression test mirroring TestGetMemberGroups_PaginatesAcrossPages: confirms
	// GetMemberWorkflowQueues' shared clmPageToCompletion loop actually issues multiple
	// requests for a member with more queues than fit on one page, rather than silently
	// returning a truncated first page. member-mallory is added via
	// AddBulkWorkflowQueueMember (not the default seed) specifically so its 105 queues
	// don't perturb the default seed's "2 distinct queues" / "10 members" assertions used
	// elsewhere.
	srv, c := clmtest.NewServer(t)
	ctx := context.Background()
	srv.AddBulkWorkflowQueueMember("member-mallory", 105)

	queues, _, err := c.GetMemberWorkflowQueues(ctx, "member-mallory")
	if err != nil {
		t.Fatalf("GetMemberWorkflowQueues: %v", err)
	}
	if len(queues) != 105 {
		t.Fatalf("expected all 105 of member-mallory's workflow queues (paginated internally), got %d", len(queues))
	}
	if got := srv.MemberWorkflowQueuesRequestCount(); got < 2 {
		t.Fatalf("expected GetMemberWorkflowQueues to issue at least 2 HTTP requests to page through 105 queues, but the mock server only saw %d — pagination is not actually happening", got)
	}
}
