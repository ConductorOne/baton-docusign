package connector

import (
	"context"
	"fmt"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

func TestClmGroupBuilder_List(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmGroupBuilder(c)
	ctx := context.Background()

	var all []*v2.Resource
	pageToken := ""
	for i := 0; i < 10; i++ {
		resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 25, Token: pageToken}})
		if err != nil {
			t.Fatalf("List page %d: %v", i, err)
		}
		all = append(all, resources...)
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	// 5 named groups + 105 synthetic bulk groups seeded for member-frank's pagination test.
	if len(all) != 110 {
		t.Fatalf("expected 110 CLM groups, got %d", len(all))
	}
}

func TestClmGroupBuilder_List_SkipsGracefullyWhenClmUnavailable(t *testing.T) {
	// See clm_members_test.go's identical test for the full rationale.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmGroupBuilder(badClient)
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

func TestClmGroupBuilder_StaticEntitlements(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmGroupBuilder(c)
	ctx := context.Background()

	ents, _, err := b.StaticEntitlements(ctx, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("StaticEntitlements: %v", err)
	}
	if len(ents) != 1 || ents[0].Slug != entitlementClmGroupMember {
		t.Fatalf("expected exactly one %q entitlement, got %+v", entitlementClmGroupMember, ents)
	}
	if len(ents[0].GrantableTo) != 1 || ents[0].GrantableTo[0].Id != clmMemberResourceType.Id {
		t.Errorf("expected the entitlement to be grantable only to clm_member, got %+v", ents[0].GrantableTo)
	}

	// Entitlements() must return nil — the SDK doesn't call it when
	// StaticEntitlementSyncerV2 is implemented, but confirm it degrades safely anyway.
	if ents2, _, err := b.Entitlements(ctx, nil, rs.SyncOpAttrs{}); err != nil || ents2 != nil {
		t.Errorf("Entitlements() should return (nil, nil, nil), got (%v, _, %v)", ents2, err)
	}
}

func TestClmGroupBuilder_Grants_Pagination(t *testing.T) {
	// Regression test: clmGroupBuilder.Grants() must thread GetGroupMembers'
	// pagination token, not just return the first page.
	_, c := clmtest.NewServer(t)
	b := newClmGroupBuilder(c)
	ctx := context.Background()

	groupResource, err := rs.NewGroupResource("Legal", clmGroupResourceType, "group-legal", nil)
	if err != nil {
		t.Fatalf("NewGroupResource: %v", err)
	}

	var all []*v2.Grant
	pageToken := ""
	for i := 0; i < 10; i++ {
		grants, res, err := b.Grants(ctx, groupResource, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 1, Token: pageToken}})
		if err != nil {
			t.Fatalf("Grants page %d: %v", i, err)
		}
		all = append(all, grants...)
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}

	// group-legal has 2 members (alice, bob) — forcing PageSize 1 requires 2 pages.
	if len(all) != 2 {
		t.Fatalf("expected 2 grants across all pages for group-legal, got %d", len(all))
	}
	for _, g := range all {
		if g.Principal.Id.ResourceType != clmMemberResourceType.Id {
			t.Errorf("expected a clm_member principal, got %s", g.Principal.Id.ResourceType)
		}
	}
}

func TestClmGroupBuilder_GrantAndRevoke_Idempotent(t *testing.T) {
	// Regression test for the same class of bug fixed in
	// TestClmFolderBuilder_GrantAndRevoke_Idempotent: GetMemberGroups is a
	// read-before-write used by both Grant and Revoke, so a cached response from an
	// earlier call in this same sequence must not be served back stale.
	srv, c := clmtest.NewServer(t)
	b := newClmGroupBuilder(c)
	ctx := context.Background()

	// member-carol starts in group-finance only (see clmtest/seed.go).
	memberResource, err := rs.NewResource("Carol", clmMemberResourceType, "member-carol")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	groupResource, err := parseIntoClmGroupResource(&client.ClmGroup{Name: "Legal", Href: srv.GroupHref("group-legal")})
	if err != nil {
		t.Fatalf("parseIntoClmGroupResource: %v", err)
	}
	ent := &v2.Entitlement{Slug: entitlementClmGroupMember, Resource: groupResource}

	// Grant once: should succeed and actually add carol to group-legal, on top of her
	// existing group-finance membership.
	if _, annos, err := b.Grant(ctx, memberResource, ent); err != nil {
		t.Fatalf("Grant: %v", err)
	} else if hasAlreadyExists(annos) {
		t.Error("first Grant should not report GrantAlreadyExists")
	}
	if groups := srv.MemberGroups("member-carol"); len(groups) != 2 {
		t.Fatalf("expected carol to be in 2 groups (finance, legal) after Grant, got %v", groups)
	}

	// Grant again: idempotent, should report GrantAlreadyExists and not duplicate.
	if _, annos, err := b.Grant(ctx, memberResource, ent); err != nil {
		t.Fatalf("second Grant: %v", err)
	} else if !hasAlreadyExists(annos) {
		t.Error("repeat Grant should report GrantAlreadyExists")
	}
	if groups := srv.MemberGroups("member-carol"); len(groups) != 2 {
		t.Fatalf("expected still exactly 2 groups after a repeat Grant, got %v", groups)
	}

	// Revoke: should remove only group-legal, preserving group-finance.
	grantObj := &v2.Grant{Principal: memberResource, Entitlement: ent}
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("Revoke: %v", err)
	} else if hasAlreadyRevoked(annos) {
		t.Error("first Revoke should not report GrantAlreadyRevoked")
	}
	groups := srv.MemberGroups("member-carol")
	if len(groups) != 1 || groups[0] != "group-finance" {
		t.Fatalf("expected carol to be back to only group-finance after Revoke, got %v", groups)
	}

	// Revoke again: idempotent.
	if annos, err := b.Revoke(ctx, grantObj); err != nil {
		t.Fatalf("second Revoke: %v", err)
	} else if !hasAlreadyRevoked(annos) {
		t.Error("repeat Revoke should report GrantAlreadyRevoked")
	}
}

// TestClmGroupBuilder_Grant_SurvivesIdentityOnlyEntitlementResource is a regression
// test for a gap the annotation-based fix in an earlier commit missed: the pebble
// storage engine's V3EntitlementToV2 (vendor/.../dotc1z/engine/pebble/translate_v2.go)
// deliberately hydrates an Entitlement's Resource as an identity-only stub on every
// read — no profile, no annotations, nothing but Id. clmGroupBuilder.Grant previously
// read the group's Href off ent.Resource; on pebble that would always fail, in every
// version of this connector including the one before this test existed. It now
// resolves the groupHref to write via clmPreferredHref — nothing on ent.Resource itself
// is ever needed, whether that resolves from a real sample Href already on hand (the
// member's other current groups) or, absent one, client.GroupHref's ID-only fallback.
// The two subtests below exercise both of clmPreferredHref's branches.
func TestClmGroupBuilder_Grant_SurvivesIdentityOnlyEntitlementResource(t *testing.T) {
	// An identity-only Resource — exactly what V3EntitlementToV2 hands back on pebble,
	// carrying nothing but the group's ID.
	identityOnlyGroupResource := clmIdentityOnlyResource(clmGroupResourceType, "group-legal")
	ent := &v2.Entitlement{Slug: entitlementClmGroupMember, Resource: identityOnlyGroupResource}

	t.Run("fallback branch: member has no other groups, derives via client.GroupHref", func(t *testing.T) {
		srv, c := clmtest.NewServer(t)
		b := newClmGroupBuilder(c)
		ctx := context.Background()

		// member-dave is seeded with zero groups (clmtest/seed.go), so currentGroups is
		// empty and clmPreferredHref has no sample to derive from.
		memberResource, err := rs.NewResource("Dave", clmMemberResourceType, "member-dave")
		if err != nil {
			t.Fatalf("NewResource: %v", err)
		}

		if _, annos, err := b.Grant(ctx, memberResource, ent); err != nil {
			t.Fatalf("Grant with an identity-only ent.Resource: %v", err)
		} else if hasAlreadyExists(annos) {
			t.Error("first Grant should not report GrantAlreadyExists")
		}
		groups := srv.MemberGroups("member-dave")
		found := false
		for _, g := range groups {
			if g == "group-legal" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected dave to be granted group-legal, got %v", groups)
		}
	})

	t.Run("sample branch: member already has another group, derives via clmPreferredHref's sample", func(t *testing.T) {
		srv, c := clmtest.NewServer(t)
		b := newClmGroupBuilder(c)
		ctx := context.Background()

		// member-carol is seeded into group-finance (clmtest/seed.go), giving
		// clmPreferredHref a real sample Href to derive group-legal's Href from instead
		// of falling back to client.GroupHref. srv.GroupHref and the fallback
		// client.GroupHref build the same shape from the same discovered base URL, so
		// they'd be byte-identical here — overriding group-finance's Href to a
		// different host makes the sample branch's output actually distinguishable from
		// what the fallback branch would have produced.
		const sampleHost = "https://other.example.com"
		altGroupFinanceHref := fmt.Sprintf("%s/v2/%s/groups/group-finance", sampleHost, clmtest.AccountID)
		srv.SetGroupHref("group-finance", altGroupFinanceHref)

		memberResource, err := rs.NewResource("Carol", clmMemberResourceType, "member-carol")
		if err != nil {
			t.Fatalf("NewResource: %v", err)
		}

		if _, annos, err := b.Grant(ctx, memberResource, ent); err != nil {
			t.Fatalf("Grant with an identity-only ent.Resource: %v", err)
		} else if hasAlreadyExists(annos) {
			t.Error("first Grant should not report GrantAlreadyExists")
		}
		groups := srv.MemberGroups("member-carol")
		found := false
		for _, g := range groups {
			if g == "group-legal" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected carol to be granted group-legal, got %v", groups)
		}

		// MemberGroups reduces every Href to its trailing ID (matching the real API's
		// own comparison semantics), which can't tell a sample-derived Href from a
		// fallback-derived one. Asserting on the raw Href the server actually received,
		// derived from the overridden group-finance sample (a different host than
		// client.GroupHref's fallback would produce), pins that clmPreferredHref
		// actually used the sample rather than falling back.
		wantHref, err := clmHrefWithID(altGroupFinanceHref, "group-legal")
		if err != nil {
			t.Fatalf("clmHrefWithID (computing expected Href): %v", err)
		}
		// PatchMemberGroups sends the member's full current+new list (additive), so the
		// PATCH body also carries carol's pre-existing group-finance entry alongside the
		// new group-legal one.
		hrefs := srv.LastPatchedMemberGroupHrefs("member-carol")
		sawWantHref := false
		for _, h := range hrefs {
			if h == wantHref {
				sawWantHref = true
			}
		}
		if !sawWantHref {
			t.Errorf("expected the PATCH request to carry Href %q, got %v", wantHref, hrefs)
		}
	})
}

// TestClmGroupMemberSlugRegressionPin guards against an accidental rename of
// the "member" entitlement slug: since clm_group is a new, unreleased
// resource type, add this before the first release locks the slug in.
// Asserts against the literal string, not entitlementClmGroupMember itself,
// so renaming the constant's value actually fails this test.
func TestClmGroupMemberSlugRegressionPin(t *testing.T) {
	const wantSlug = "member"
	if entitlementClmGroupMember != wantSlug {
		t.Fatalf("entitlementClmGroupMember slug changed: got %q, want %q", entitlementClmGroupMember, wantSlug)
	}
}
