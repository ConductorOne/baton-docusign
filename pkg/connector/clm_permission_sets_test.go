package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

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
	if len(ents[0].GrantableTo) != 2 {
		t.Errorf("expected the entitlement to be grantable to clm_member and clm_group, got %+v", ents[0].GrantableTo)
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
