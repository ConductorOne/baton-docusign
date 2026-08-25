package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

func TestClmPermissionSetBuilder_List_FailsWhenClmUnavailable(t *testing.T) {
	// See clm_members_test.go's identical test for the full rationale.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmPermissionSetBuilder(badClient)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err == nil {
		t.Fatal("expected List to fail when CLM is unavailable, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

func TestClmPermissionSetBuilder_List_Pagination(t *testing.T) {
	// Regression test: clmPermissionSetBuilder.List() previously didn't thread
	// ListPermissionSets' pagination token and always returned only the first page.
	_, c := clmtest.NewServer(t)
	b := newClmPermissionSetBuilder(c)
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

	if len(all) != 5 {
		t.Fatalf("expected 5 CLM permission sets across all pages, got %d", len(all))
	}
}

func TestClmPermissionSetBuilder_Entitlements(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmPermissionSetBuilder(c)
	ctx := context.Background()

	psResource, err := rs.NewRoleResource("Administrator", clmPermissionSetResourceType, "ps-admin", nil)
	if err != nil {
		t.Fatalf("NewRoleResource: %v", err)
	}

	ents, _, err := b.Entitlements(ctx, psResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Entitlements: %v", err)
	}
	if len(ents) != 1 || ents[0].Slug != clmPermissionSetAssignedTag {
		t.Fatalf("expected exactly one %q entitlement, got %+v", clmPermissionSetAssignedTag, ents)
	}
	// No WithGrantableTo: there's no Grant/Revoke path anywhere in this connector for
	// CLM permission sets, so declaring one would show this as assignable in the C1 UI
	// when it never actually can be.
	if len(ents[0].GrantableTo) != 0 {
		t.Errorf("expected no GrantableTo (visibility-only entitlement), got %+v", ents[0].GrantableTo)
	}
}

func TestClmPermissionSetBuilder_Grants_IsAlwaysEmpty(t *testing.T) {
	// Confirmed unsupported by the API: no endpoint links a member/group to a
	// permission set as an assignment, so Grants() must be a hard no-op, not an error.
	_, c := clmtest.NewServer(t)
	b := newClmPermissionSetBuilder(c)
	ctx := context.Background()

	psResource, err := rs.NewRoleResource("Administrator", clmPermissionSetResourceType, "ps-admin", nil)
	if err != nil {
		t.Fatalf("NewRoleResource: %v", err)
	}

	grants, res, err := b.Grants(ctx, psResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if grants != nil || res != nil {
		t.Errorf("expected Grants to return (nil, nil, nil), got (%v, %v, nil)", grants, res)
	}
}

// TestClmPermissionSetAssignedTagSlugRegressionPin guards against an
// accidental rename of the "assigned" entitlement slug: since
// clm_permission_set is a new, unreleased resource type, add this before the
// first release locks the slug in. Asserts against the literal string, not
// clmPermissionSetAssignedTag itself, so renaming the constant's value
// actually fails this test.
func TestClmPermissionSetAssignedTagSlugRegressionPin(t *testing.T) {
	const wantSlug = "assigned"
	if clmPermissionSetAssignedTag != wantSlug {
		t.Fatalf("clmPermissionSetAssignedTag slug changed: got %q, want %q", clmPermissionSetAssignedTag, wantSlug)
	}
}
