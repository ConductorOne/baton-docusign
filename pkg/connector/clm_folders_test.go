package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

func boolPtr(b bool) *bool { return &b }

// --- Pure-function tests: clmSlugForEntry / clmAccessTypeForSlug / clmPrincipalIDForItem ---
//
// These map the CLM API's two representations of folder access (a named AccessType
// enum on writes, granular boolean flags on reads) onto the 5 static entitlement
// slugs, and classify a Security entry's Item into a principal. Getting these wrong
// silently mis-grants or drops access in a security review, so they're covered
// directly rather than only through the higher-level Grants()/Grant() tests below.

func TestClmSlugForEntry_AccessTypeBased(t *testing.T) {
	tests := []struct {
		accessType string
		wantSlug   string
		wantOK     bool
	}{
		{client.ClmAccessTypeView, "view", true},
		{client.ClmAccessTypeViewCreate, "view_create", true},
		{client.ClmAccessTypeViewEdit, "view_edit", true},
		{client.ClmAccessTypeViewEditDelete, "view_edit_delete", true},
		{client.ClmAccessTypeViewEditDeleteSetAccess, "view_edit_delete_set_access", true},
		{client.ClmAccessTypeNoAccess, "", false},
		{client.ClmAccessTypeInherit, "", false},
		{client.ClmAccessTypeCustom, "", false},
	}
	for _, tt := range tests {
		slug, ok := clmSlugForEntry(client.ClmSecurityEntry{AccessType: tt.accessType})
		if ok != tt.wantOK || slug != tt.wantSlug {
			t.Errorf("clmSlugForEntry(AccessType=%q) = (%q, %v), want (%q, %v)", tt.accessType, slug, ok, tt.wantSlug, tt.wantOK)
		}
	}
}

func TestClmSlugForEntry_FlagsBased(t *testing.T) {
	tests := []struct {
		name                                      string
		create, move, read, see, setAccess, write bool
		wantSlug                                  string
		wantOK                                    bool
	}{
		{"view: read+see only", false, false, true, true, false, false, "view", true},
		{"view_create: +create", true, false, true, true, false, false, "view_create", true},
		{"view_edit: +write", true, false, true, true, false, true, "view_edit", true},
		{"view_edit_delete: +move", true, true, true, true, false, true, "view_edit_delete", true},
		{"view_edit_delete_set_access: all flags", true, true, true, true, true, true, "view_edit_delete_set_access", true},
		{"no read/see: not a tier", false, false, false, false, false, false, "", false},
		{"read without see: not a tier", false, false, true, false, false, false, "", false},
		{"create without read/see: the Custom case", true, false, false, false, false, false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := client.ClmSecurityEntry{
				Create: boolPtr(tt.create), Move: boolPtr(tt.move), Read: boolPtr(tt.read),
				See: boolPtr(tt.see), SetAccess: boolPtr(tt.setAccess), Write: boolPtr(tt.write),
			}
			slug, ok := clmSlugForEntry(entry)
			if ok != tt.wantOK || slug != tt.wantSlug {
				t.Errorf("clmSlugForEntry(%+v) = (%q, %v), want (%q, %v)", tt, slug, ok, tt.wantSlug, tt.wantOK)
			}
		})
	}
}

func TestClmAccessTypeForSlug_RoundTrips(t *testing.T) {
	for _, fe := range clmFolderEntitlements {
		got, ok := clmAccessTypeForSlug(fe.slug)
		if !ok || got != fe.accessType {
			t.Errorf("clmAccessTypeForSlug(%q) = (%q, %v), want (%q, true)", fe.slug, got, ok, fe.accessType)
		}
	}
	if _, ok := clmAccessTypeForSlug("not_a_real_slug"); ok {
		t.Error("expected clmAccessTypeForSlug to reject an unknown slug")
	}
}

func TestClmNormalizeSecurityEntryForWrite(t *testing.T) {
	t.Run("AccessType already set passes through unchanged", func(t *testing.T) {
		entry := client.ClmSecurityEntry{AccessType: client.ClmAccessTypeViewEdit, Item: "group-legal"}
		got := clmNormalizeSecurityEntryForWrite(entry)
		if got != entry {
			t.Errorf("expected an AccessType-based entry to pass through unchanged, got %+v", got)
		}
	})

	t.Run("resolvable flags-only entry is normalized to AccessType", func(t *testing.T) {
		entry := client.ClmSecurityEntry{Item: "member-bob", Read: boolPtr(true), See: boolPtr(true)}
		got := clmNormalizeSecurityEntryForWrite(entry)
		want := client.ClmSecurityEntry{AccessType: client.ClmAccessTypeView, Item: "member-bob"}
		if got != want {
			t.Errorf("clmNormalizeSecurityEntryForWrite(%+v) = %+v, want %+v", entry, got, want)
		}
	})

	t.Run("unresolvable (Custom) flags-only entry passes through unchanged", func(t *testing.T) {
		entry := client.ClmSecurityEntry{Item: "group-finance", Create: boolPtr(true)}
		got := clmNormalizeSecurityEntryForWrite(entry)
		if *got.Create != *entry.Create || got.Item != entry.Item || got.AccessType != "" {
			t.Errorf("expected an unresolvable flags entry to pass through unchanged, got %+v", got)
		}
	})
}

func TestClmPrincipalIDForItem(t *testing.T) {
	srv, _ := clmtest.NewServer(t)

	t.Run("role name maps to clm_role", func(t *testing.T) {
		id, ok := clmPrincipalIDForItem("FullSubscriber")
		if !ok || id.ResourceType != clmRoleResourceType.Id || id.Resource != "FullSubscriber" {
			t.Errorf("got (%+v, %v), want clm_role/FullSubscriber", id, ok)
		}
	})

	t.Run("group href maps to clm_group", func(t *testing.T) {
		href := srv.GroupHref("group-legal")
		id, ok := clmPrincipalIDForItem(href)
		if !ok || id.ResourceType != clmGroupResourceType.Id || id.Resource != "group-legal" {
			t.Errorf("got (%+v, %v), want clm_group/group-legal", id, ok)
		}
	})

	t.Run("member href maps to clm_member", func(t *testing.T) {
		href := srv.MemberHref("member-bob")
		id, ok := clmPrincipalIDForItem(href)
		if !ok || id.ResourceType != clmMemberResourceType.Id || id.Resource != "member-bob" {
			t.Errorf("got (%+v, %v), want clm_member/member-bob", id, ok)
		}
	})

	t.Run("unrecognized item is rejected, not guessed", func(t *testing.T) {
		if _, ok := clmPrincipalIDForItem("not-a-role-or-href"); ok {
			t.Error("expected an unrecognized Item to be rejected")
		}
	})
}

// --- Integration tests against the clmtest mock server ---

func TestClmFolderBuilder_List_SkipsWithoutAnyClientCallWhenIncludeClmUnset(t *testing.T) {
	// See clm_members_test.go's identical test for the full rationale. A nil client
	// proves the guard fires before any client call — the 401-tolerance test below
	// only proves tolerance of a specific error *after* the client is reached.
	b := newClmFolderBuilder(nil, false)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when includeClm is unset, got %d", len(resources))
	}
	if res == nil || res.NextPageToken != "" {
		t.Errorf("expected an empty (non-paginating) result, got %+v", res)
	}
}

func TestClmFolderBuilder_List_SkipsGracefullyWhenClmUnavailable(t *testing.T) {
	// See clm_members_test.go's identical test for the full rationale.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmFolderBuilder(badClient, true)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err != nil {
		t.Fatalf("expected List to tolerate an unavailable CLM account and skip gracefully, got error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources when CLM is unavailable, got %d", len(resources))
	}
	if res == nil || res.NextPageToken != "" {
		t.Errorf("expected an empty (non-paginating) result, got %+v", res)
	}
}

func TestClmFolderBuilder_List(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c, true)
	ctx := context.Background()

	var all []*v2.Resource
	pageToken := ""
	for i := 0; i < 10; i++ {
		resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 2, Token: pageToken}})
		if err != nil {
			t.Fatalf("List page %d: %v", i, err)
		}
		all = append(all, resources...)
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	if len(all) != 3 {
		t.Fatalf("expected 3 CLM folders (root, templates, contracts), got %d", len(all))
	}
}

func TestClmFolderBuilder_StaticEntitlements(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c, true)
	ctx := context.Background()

	ents, _, err := b.StaticEntitlements(ctx, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("StaticEntitlements: %v", err)
	}
	if len(ents) != 5 {
		t.Fatalf("expected exactly 5 static entitlements (the grantable tiers), got %d", len(ents))
	}
	for _, e := range ents {
		if len(e.GrantableTo) != 3 {
			t.Errorf("entitlement %q: expected grantable to clm_member/clm_group/clm_role (3 types), got %d", e.Slug, len(e.GrantableTo))
		}
	}

	// Entitlements() must return nil — the SDK doesn't call it when
	// StaticEntitlementSyncerV2 is implemented, but confirm it degrades safely anyway.
	if ents2, _, err := b.Entitlements(ctx, nil, rs.SyncOpAttrs{}); err != nil || ents2 != nil {
		t.Errorf("Entitlements() should return (nil, nil, nil), got (%v, _, %v)", ents2, err)
	}
}

func TestClmFolderBuilder_Grants_MapsAndSkipsCorrectly(t *testing.T) {
	// folder-contracts is seeded with 4 Security entries: an AccessType-based one
	// (group), a flags-based one (member), a role-granted one, and one that matches no
	// known tier ("Custom" — Create=true only). Grants() must emit exactly 3 grants,
	// skipping the unmatched one rather than approximating it.
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c, true)
	ctx := context.Background()

	folderResource, err := rs.NewResource("Contracts", clmFolderResourceType, "folder-contracts")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	grants, _, err := b.Grants(ctx, folderResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 3 {
		t.Fatalf("expected 3 grants (the Custom entry should be skipped), got %d", len(grants))
	}

	var sawGroup, sawMember, sawRole bool
	for _, g := range grants {
		switch g.Principal.Id.ResourceType {
		case clmGroupResourceType.Id:
			sawGroup = true
			if g.Principal.Id.Resource != "group-legal" {
				t.Errorf("expected the group grant to target group-legal, got %s", g.Principal.Id.Resource)
			}
			if len(g.Annotations) == 0 {
				t.Error("expected a GrantExpandable annotation on the group-principal grant")
			}
		case clmMemberResourceType.Id:
			sawMember = true
			if g.Principal.Id.Resource != "member-bob" {
				t.Errorf("expected the member grant to target member-bob, got %s", g.Principal.Id.Resource)
			}
		case clmRoleResourceType.Id:
			sawRole = true
			if g.Principal.Id.Resource != "FullSubscriber" {
				t.Errorf("expected the role grant to target FullSubscriber, got %s", g.Principal.Id.Resource)
			}
		}
	}
	if !sawGroup || !sawMember || !sawRole {
		t.Errorf("expected one grant each for group/member/role principals; got group=%v member=%v role=%v", sawGroup, sawMember, sawRole)
	}
	_ = srv
}

func TestClmFolderBuilder_GrantAndRevoke_Idempotent(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c, true)
	ctx := context.Background()

	// folder-templates starts with no Security entries.
	folderResource, err := rs.NewResource("Templates", clmFolderResourceType, "folder-templates")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	groupResource, err := parseIntoClmGroupResource(&client.ClmGroup{Name: "Operations", Href: srv.GroupHref("group-ops")})
	if err != nil {
		t.Fatalf("parseIntoClmGroupResource: %v", err)
	}

	ent := &v2.Entitlement{Slug: "view_edit", Resource: folderResource}

	// Grant once: should succeed and actually write the entry.
	if _, annos, err := b.Grant(ctx, groupResource, ent); err != nil {
		t.Fatalf("Grant: %v", err)
	} else if hasAlreadyExists(annos) {
		t.Error("first Grant should not report GrantAlreadyExists")
	}
	entries := srv.FolderSecurity("folder-templates")
	if len(entries) != 1 || entries[0].AccessType != client.ClmAccessTypeViewEdit {
		t.Fatalf("expected one ViewEdit entry after Grant, got %+v", entries)
	}

	// Grant again: idempotent, should report GrantAlreadyExists and not duplicate.
	if _, annos, err := b.Grant(ctx, groupResource, ent); err != nil {
		t.Fatalf("second Grant: %v", err)
	} else if !hasAlreadyExists(annos) {
		t.Error("repeat Grant should report GrantAlreadyExists")
	}
	if entries := srv.FolderSecurity("folder-templates"); len(entries) != 1 {
		t.Fatalf("expected still exactly one entry after a repeat Grant, got %d", len(entries))
	}

	// Revoke: should set AccessType to NoAccess (not remove the entry).
	grantObj := &v2.Grant{Principal: groupResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Error("first Revoke should not report GrantAlreadyRevoked")
	}
	entries = srv.FolderSecurity("folder-templates")
	if len(entries) != 1 || entries[0].AccessType != client.ClmAccessTypeNoAccess {
		t.Fatalf("expected the entry's AccessType to become NoAccess after Revoke, got %+v", entries)
	}

	// Revoke again: idempotent.
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("second Revoke: %v", err)
	} else if !hasAlreadyRevoked(annos) {
		t.Error("repeat Revoke should report GrantAlreadyRevoked")
	}
}

// TestClmFolderBuilder_GrantAndRevoke_PreservesOtherPrincipals is a regression test
// for a folder-wide data-loss risk: Folders.Patch's merge-vs-replace semantics for the
// Security field are undocumented, so Grant/Revoke always send the folder's complete
// Security list back (via clmFolderSecurityWithEntry), not just the one changed entry.
// The clmtest mock models the pessimistic "replace" interpretation specifically to
// catch a regression to sending just one entry — that would make this test fail with
// the other 3 principals' entries disappearing. folder-contracts is seeded with 4
// entries (see seed.go); this grants and revokes access for a 5th, previously-absent
// principal ("group-ops") and confirms the original 4 are untouched throughout.
func TestClmFolderBuilder_GrantAndRevoke_PreservesOtherPrincipals(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c, true)
	ctx := context.Background()

	before := srv.FolderSecurity("folder-contracts")
	if len(before) != 4 {
		t.Fatalf("expected folder-contracts seeded with 4 entries, got %d: %+v", len(before), before)
	}

	folderResource, err := rs.NewResource("Contracts", clmFolderResourceType, "folder-contracts")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	groupResource, err := parseIntoClmGroupResource(&client.ClmGroup{Name: "Operations", Href: srv.GroupHref("group-ops")})
	if err != nil {
		t.Fatalf("parseIntoClmGroupResource: %v", err)
	}
	ent := &v2.Entitlement{Slug: "view", Resource: folderResource}

	if _, annos, err := b.Grant(ctx, groupResource, ent); err != nil {
		t.Fatalf("Grant: %v", err)
	} else if hasAlreadyExists(annos) {
		t.Error("first Grant should not report GrantAlreadyExists")
	}

	afterGrant := srv.FolderSecurity("folder-contracts")
	if len(afterGrant) != 5 {
		t.Fatalf("expected 5 entries after granting a 5th principal, got %d: %+v", len(afterGrant), afterGrant)
	}
	for _, want := range before {
		got, ok := findSecurityEntryByItem(afterGrant, want.Item)
		if !ok {
			t.Errorf("expected pre-existing entry for %s to survive Grant, but it's gone; got %+v", want.Item, afterGrant)
			continue
		}
		assertSameEffectiveAccess(t, want, got, "Grant")
	}

	grantObj := &v2.Grant{Principal: groupResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Error("first Revoke should not report GrantAlreadyRevoked")
	}

	afterRevoke := srv.FolderSecurity("folder-contracts")
	if len(afterRevoke) != 5 {
		t.Fatalf("expected still 5 entries after Revoke (NoAccess, not removed), got %d: %+v", len(afterRevoke), afterRevoke)
	}
	for _, want := range before {
		got, ok := findSecurityEntryByItem(afterRevoke, want.Item)
		if !ok {
			t.Errorf("expected pre-existing entry for %s to survive Revoke, but it's gone; got %+v", want.Item, afterRevoke)
			continue
		}
		assertSameEffectiveAccess(t, want, got, "Revoke")
	}
}

// findSecurityEntryByItem finds the entry for the same principal as item, comparing
// via clmIDFromHref like Grant/Revoke's own existence checks (a bare ID and a Href
// ending in that ID must still match).
func findSecurityEntryByItem(entries []client.ClmSecurityEntry, item string) (client.ClmSecurityEntry, bool) {
	for _, e := range entries {
		if clmIDFromHref(e.Item) == clmIDFromHref(item) {
			return e, true
		}
	}
	return client.ClmSecurityEntry{}, false
}

// assertSameEffectiveAccess confirms want and got resolve to the same effective tier
// via clmSlugForEntry, rather than requiring exact struct equality: preserved entries
// get normalized from their read-side shape (flags, no AccessType) to the write-side
// shape (AccessType) by clmNormalizeSecurityEntryForWrite, so a flags-based entry and
// its AccessType-based equivalent are expected to differ byte-for-byte while
// representing the same access.
func assertSameEffectiveAccess(t *testing.T, want, got client.ClmSecurityEntry, when string) {
	t.Helper()
	wantSlug, wantOK := clmSlugForEntry(want)
	gotSlug, gotOK := clmSlugForEntry(got)
	if wantOK != gotOK || wantSlug != gotSlug {
		t.Errorf("after %s: entry for %s changed effective access — before: %+v (slug=%q resolvable=%v), after: %+v (slug=%q resolvable=%v)",
			when, want.Item, want, wantSlug, wantOK, got, gotSlug, gotOK)
	}
}

// TestClmFolderBuilder_GrantAndRevoke_ToleratesBareIDOnRead is a regression test: the
// CLM API's read-side Item representation isn't confirmed to always match the exact
// Href shape clmItemForPrincipal constructs on the write side (see clmPrincipalIDForItem,
// which normalizes for the same reason). Grant/Revoke's existence checks used to compare
// entry.Item to item with raw string equality — if the API ever returns Item as a bare
// ID instead of a full Href, that comparison never matches, and Revoke in particular
// would silently report GrantAlreadyRevoked without ever patching AccessType to
// NoAccess, leaving the grant active. This seeds a folder-security entry with a bare-ID
// Item directly (bypassing Grant) to simulate that read-side shape.
func TestClmFolderBuilder_GrantAndRevoke_ToleratesBareIDOnRead(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c, true)
	ctx := context.Background()

	// Seed folder-templates' Security entry with a bare group ID, not the full Href
	// clmItemForPrincipal would construct.
	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", []client.ClmSecurityEntry{{
		AccessType: client.ClmAccessTypeViewEdit,
		Item:       "group-ops",
	}}); err != nil {
		t.Fatalf("PatchFolderSecurity (seed): %v", err)
	}

	folderResource, err := rs.NewResource("Templates", clmFolderResourceType, "folder-templates")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	groupResource, err := parseIntoClmGroupResource(&client.ClmGroup{Name: "Operations", Href: srv.GroupHref("group-ops")})
	if err != nil {
		t.Fatalf("parseIntoClmGroupResource: %v", err)
	}
	ent := &v2.Entitlement{Slug: "view_edit", Resource: folderResource}

	// Grant for the same tier must recognize the bare-ID entry as already granted.
	if _, annos, err := b.Grant(ctx, groupResource, ent); err != nil {
		t.Fatalf("Grant: %v", err)
	} else if !hasAlreadyExists(annos) {
		t.Error("Grant should recognize a bare-ID entry.Item as already granted, not duplicate it")
	}
	if entries := srv.FolderSecurity("folder-templates"); len(entries) != 1 {
		t.Fatalf("expected still exactly one entry, got %d: %+v", len(entries), entries)
	}

	// Revoke must actually patch AccessType to NoAccess, not silently no-op.
	grantObj := &v2.Grant{Principal: groupResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Fatal("Revoke incorrectly reported GrantAlreadyRevoked for a bare-ID entry.Item — access was left in place instead of being revoked")
	}
	entries := srv.FolderSecurity("folder-templates")
	if len(entries) != 1 || entries[0].AccessType != client.ClmAccessTypeNoAccess {
		t.Fatalf("expected the entry's AccessType to become NoAccess after Revoke, got %+v", entries)
	}
}

func hasAlreadyExists(annos annotations.Annotations) bool {
	return annos.Contains(&v2.GrantAlreadyExists{})
}

func hasAlreadyRevoked(annos annotations.Annotations) bool {
	return annos.Contains(&v2.GrantAlreadyRevoked{})
}
