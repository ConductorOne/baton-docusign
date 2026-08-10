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

// --- Pure-function tests: clmSlugForAccessType / clmAccessTypeForSlug ---
//
// These map the CLM API's AccessType enum onto the 5 static entitlement slugs.
// Getting this wrong silently mis-grants or drops access in a security review, so
// it's covered directly rather than only through the higher-level Grants()/Grant()
// tests below.

func TestClmSlugForAccessType(t *testing.T) {
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
		{"", "", false},
	}
	for _, tt := range tests {
		slug, ok := clmSlugForAccessType(tt.accessType)
		if ok != tt.wantOK || slug != tt.wantSlug {
			t.Errorf("clmSlugForAccessType(%q) = (%q, %v), want (%q, %v)", tt.accessType, slug, ok, tt.wantSlug, tt.wantOK)
		}
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

// --- Integration tests against the clmtest mock server ---

func TestClmFolderBuilder_List_SkipsGracefullyWhenClmUnavailable(t *testing.T) {
	// See clm_members_test.go's identical test for the full rationale.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmFolderBuilder(badClient)
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
	b := newClmFolderBuilder(c)
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
	b := newClmFolderBuilder(c)
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
	// folder-contracts is seeded with 4 Security entries across all 3 principal-type
	// collections (see seed.go): a known-tier group, a "Custom" (unmapped) group, a
	// known-tier role, and a known-tier member. Grants() must emit exactly 3 grants,
	// skipping the Custom entry rather than approximating it.
	_, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c)
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
		t.Fatalf("expected 3 grants (the Custom group entry should be skipped), got %d", len(grants))
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
}

// TestClmFolderBuilder_Grants_SkipsUnknownRoleName is a regression test: clm_role is a
// fixed, hardcoded 5-role list (clmRoleBuilder.List, not fetched from the API), so a
// Roles security entry referencing a role name outside that set has no synced
// principal to grant against. The old clmPrincipalIDForItem validated role names
// before emitting a grant; the rewritten Grants() must do the same rather than
// blindly trusting entry.Item.
func TestClmFolderBuilder_Grants_SkipsUnknownRoleName(t *testing.T) {
	_, c := clmtest.NewServer(t)
	ctx := context.Background()

	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Roles: []client.ClmRoleSecurityEntry{{AccessType: client.ClmAccessTypeView, Item: "NotARealRole"}},
	}); err != nil {
		t.Fatalf("PatchFolderSecurity (seed): %v", err)
	}

	b := newClmFolderBuilder(c)
	folderResource, err := rs.NewResource("Templates", clmFolderResourceType, "folder-templates")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	grants, _, err := b.Grants(ctx, folderResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("expected the unknown role entry to be skipped, got %d grants: %+v", len(grants), grants)
	}
}

func TestClmFolderBuilder_GrantAndRevoke_Idempotent(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c)
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
	groups := srv.FolderSecurity("folder-templates").Groups.Items
	if len(groups) != 1 || groups[0].AccessType != client.ClmAccessTypeViewEdit {
		t.Fatalf("expected one ViewEdit group entry after Grant, got %+v", groups)
	}

	// Grant again: idempotent, should report GrantAlreadyExists and not duplicate.
	if _, annos, err := b.Grant(ctx, groupResource, ent); err != nil {
		t.Fatalf("second Grant: %v", err)
	} else if !hasAlreadyExists(annos) {
		t.Error("repeat Grant should report GrantAlreadyExists")
	}
	if groups := srv.FolderSecurity("folder-templates").Groups.Items; len(groups) != 1 {
		t.Fatalf("expected still exactly one entry after a repeat Grant, got %d", len(groups))
	}

	// Revoke: should set AccessType to NoAccess (not remove the entry).
	grantObj := &v2.Grant{Principal: groupResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Error("first Revoke should not report GrantAlreadyRevoked")
	}
	groups = srv.FolderSecurity("folder-templates").Groups.Items
	if len(groups) != 1 || groups[0].AccessType != client.ClmAccessTypeNoAccess {
		t.Fatalf("expected the entry's AccessType to become NoAccess after Revoke, got %+v", groups)
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
// security state back (via clmFolderSecurityToWrite), not just the one changed entry.
// The clmtest mock models the pessimistic "replace" interpretation specifically to
// catch a regression to sending just one entry — that would make this test fail with
// the other principals' entries disappearing. folder-contracts is seeded with 4
// entries across all 3 collections (see seed.go); this grants and revokes access for a
// 5th, previously-absent principal ("group-ops") and confirms the original 4 are
// untouched throughout.
func TestClmFolderBuilder_GrantAndRevoke_PreservesOtherPrincipals(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c)
	ctx := context.Background()

	before := srv.FolderSecurity("folder-contracts")
	if total := len(before.Groups.Items) + len(before.Roles.Items) + len(before.Users.Items); total != 4 {
		t.Fatalf("expected folder-contracts seeded with 4 entries total, got %d: %+v", total, before)
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
	if len(afterGrant.Groups.Items) != 3 { // group-legal, group-finance, + the new group-ops
		t.Fatalf("expected 3 group entries after granting a new group principal, got %d: %+v", len(afterGrant.Groups.Items), afterGrant.Groups.Items)
	}
	assertGroupsPreserved(t, before.Groups.Items, afterGrant.Groups.Items, "Grant")
	assertRolesPreserved(t, before.Roles.Items, afterGrant.Roles.Items, "Grant")
	assertUsersPreserved(t, before.Users.Items, afterGrant.Users.Items, "Grant")

	grantObj := &v2.Grant{Principal: groupResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Error("first Revoke should not report GrantAlreadyRevoked")
	}

	afterRevoke := srv.FolderSecurity("folder-contracts")
	if len(afterRevoke.Groups.Items) != 3 { // still 3 (NoAccess, not removed)
		t.Fatalf("expected still 3 group entries after Revoke (NoAccess, not removed), got %d: %+v", len(afterRevoke.Groups.Items), afterRevoke.Groups.Items)
	}
	assertGroupsPreserved(t, before.Groups.Items, afterRevoke.Groups.Items, "Revoke")
	assertRolesPreserved(t, before.Roles.Items, afterRevoke.Roles.Items, "Revoke")
	assertUsersPreserved(t, before.Users.Items, afterRevoke.Users.Items, "Revoke")
}

func assertGroupsPreserved(t *testing.T, before, after []client.ClmGroupSecurityEntry, when string) {
	t.Helper()
	for _, want := range before {
		i := clmFindGroupSecurityIndex(after, want.Href)
		if i < 0 {
			t.Errorf("expected pre-existing group entry for %s to survive %s, but it's gone; got %+v", want.Href, when, after)
			continue
		}
		if after[i].AccessType != want.AccessType {
			t.Errorf("after %s: group entry for %s changed AccessType — before: %q, after: %q", when, want.Href, want.AccessType, after[i].AccessType)
		}
	}
}

func assertRolesPreserved(t *testing.T, before, after []client.ClmRoleSecurityEntry, when string) {
	t.Helper()
	for _, want := range before {
		i := clmFindRoleSecurityIndex(after, want.Item)
		if i < 0 {
			t.Errorf("expected pre-existing role entry for %s to survive %s, but it's gone; got %+v", want.Item, when, after)
			continue
		}
		if after[i].AccessType != want.AccessType {
			t.Errorf("after %s: role entry for %s changed AccessType — before: %q, after: %q", when, want.Item, want.AccessType, after[i].AccessType)
		}
	}
}

func assertUsersPreserved(t *testing.T, before, after []client.ClmUserSecurityEntry, when string) {
	t.Helper()
	for _, want := range before {
		i := clmFindUserSecurityIndex(after, want.Href)
		if i < 0 {
			t.Errorf("expected pre-existing user entry for %s to survive %s, but it's gone; got %+v", want.Href, when, after)
			continue
		}
		if after[i].AccessType != want.AccessType {
			t.Errorf("after %s: user entry for %s changed AccessType — before: %q, after: %q", when, want.Href, want.AccessType, after[i].AccessType)
		}
	}
}

// TestClmFolderBuilder_GrantAndRevoke_ToleratesBareIDOnRead is a regression test: the
// CLM API's read-side Href representation isn't confirmed to always match the exact
// Href shape clmGroupHrefFromResource constructs on the write side. Grant/Revoke's
// existence checks compare via clmIDFromHref (not raw equality) specifically so a bare
// ID and a full Href ending in that ID are still treated as the same principal — if
// the API ever returns Href as a bare ID, a raw-equality comparison would never match,
// and Revoke in particular would silently report GrantAlreadyRevoked without ever
// patching AccessType to NoAccess, leaving the grant active. This seeds a
// folder-security entry with a bare-ID Href directly (bypassing Grant) to simulate
// that read-side shape.
func TestClmFolderBuilder_GrantAndRevoke_ToleratesBareIDOnRead(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c)
	ctx := context.Background()

	// Seed folder-templates' group Security entry with a bare group ID, not the full
	// Href clmGroupHrefFromResource would construct.
	if _, err := c.PatchFolderSecurity(ctx, "folder-templates", client.ClmFolderSecurityWrite{
		Groups: []client.ClmGroupSecurityEntry{{AccessType: client.ClmAccessTypeViewEdit, Href: "group-ops"}},
	}); err != nil {
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
		t.Error("Grant should recognize a bare-ID entry.Href as already granted, not duplicate it")
	}
	if groups := srv.FolderSecurity("folder-templates").Groups.Items; len(groups) != 1 {
		t.Fatalf("expected still exactly one entry, got %d: %+v", len(groups), groups)
	}

	// Revoke must actually patch AccessType to NoAccess, not silently no-op.
	grantObj := &v2.Grant{Principal: groupResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Fatal("Revoke incorrectly reported GrantAlreadyRevoked for a bare-ID entry.Href — access was left in place instead of being revoked")
	}
	groups := srv.FolderSecurity("folder-templates").Groups.Items
	if len(groups) != 1 || groups[0].AccessType != client.ClmAccessTypeNoAccess {
		t.Fatalf("expected the entry's AccessType to become NoAccess after Revoke, got %+v", groups)
	}
}

// clmIdentityOnlyResource builds a Resource carrying nothing but its Id — the worst
// case any code path in this connector can hand Grant/Revoke a principal in: it's what
// the SDK's local file-mode provisioner effectively reduces a principal to once you
// strip the fields no longer relied on (see the previous commit), and it's exactly what
// the pebble storage engine's V3EntitlementToV2 (vendor/.../dotc1z/engine/pebble/
// translate_v2.go) hydrates an Entitlement's own Resource as on every read, by design,
// regardless of provisioning path. clmMemberHrefFromResource/clmGroupHrefFromResource
// no longer exist — Grant/Revoke derive the Href straight from resourceID via
// client.GroupHref/client.MemberHref, so nothing beyond Id is ever needed.
func clmIdentityOnlyResource(resourceType *v2.ResourceType, resourceID string) *v2.Resource {
	return &v2.Resource{
		Id: &v2.ResourceId{ResourceType: resourceType.Id, Resource: resourceID},
	}
}

// TestClmFolderBuilder_GrantAndRevoke_SurvivesIdentityOnlyPrincipal is a regression test
// for the bug originally fixed by carrying the CLM href on the resource (first via
// ExternalId, then an annotation) and, after the pebble-engine gap the annotation
// approach missed, fixed properly by deriving the Href from the ID instead. Every other
// Grant/Revoke test in this file passes the fully-populated resource
// parseIntoClm*Resource returns — so it would pass whether or not either fix actually
// worked. This test instead passes an identity-only principal — see
// clmIdentityOnlyResource — to confirm the Href is still derivable with nothing else on
// the resource to fall back to.
func TestClmFolderBuilder_GrantAndRevoke_SurvivesIdentityOnlyPrincipal(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	b := newClmFolderBuilder(c)
	ctx := context.Background()

	folderResource, err := rs.NewResource("Templates", clmFolderResourceType, "folder-templates")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	t.Run("clm_group principal", func(t *testing.T) {
		principal := clmIdentityOnlyResource(clmGroupResourceType, "group-ops")
		ent := &v2.Entitlement{Slug: "view", Resource: folderResource}

		if _, _, err := b.Grant(ctx, principal, ent); err != nil {
			t.Fatalf("Grant with an identity-only principal: %v", err)
		}
		groups := srv.FolderSecurity("folder-templates").Groups.Items
		if len(groups) != 1 || groups[0].AccessType != client.ClmAccessTypeView {
			t.Fatalf("expected one View group entry after Grant, got %+v", groups)
		}

		grantObj := &v2.Grant{Principal: principal, Entitlement: ent}
		if _, err := b.Revoke(ctx, grantObj); err != nil {
			t.Fatalf("Revoke with an identity-only principal: %v", err)
		}
	})

	t.Run("clm_member principal", func(t *testing.T) {
		principal := clmIdentityOnlyResource(clmMemberResourceType, "member-dave")
		ent := &v2.Entitlement{Slug: "view", Resource: folderResource}

		if _, _, err := b.Grant(ctx, principal, ent); err != nil {
			t.Fatalf("Grant with an identity-only principal: %v", err)
		}
		users := srv.FolderSecurity("folder-templates").Users.Items
		if len(users) != 1 || users[0].AccessType != client.ClmAccessTypeView {
			t.Fatalf("expected one View user entry after Grant, got %+v", users)
		}

		grantObj := &v2.Grant{Principal: principal, Entitlement: ent}
		if _, err := b.Revoke(ctx, grantObj); err != nil {
			t.Fatalf("Revoke with an identity-only principal: %v", err)
		}
	})
}

func hasAlreadyExists(annos annotations.Annotations) bool {
	return annos.Contains(&v2.GrantAlreadyExists{})
}

func hasAlreadyRevoked(annos annotations.Annotations) bool {
	return annos.Contains(&v2.GrantAlreadyRevoked{})
}
