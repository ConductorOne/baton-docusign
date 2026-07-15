package connector

import (
	"context"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
)

func TestClmMemberBuilder_List_Pagination(t *testing.T) {
	// member-frank has 105 synthetic groups but is just one member row among 6 — this
	// confirms ListMembers' own pagination (not GetMemberGroups') is threaded correctly.
	_, c := clmtest.NewServer(t)
	b := newClmMemberBuilder(c)
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

	if len(all) != 6 { // alice, bob, carol, dave, eve, frank
		t.Fatalf("expected 6 CLM members across all pages, got %d", len(all))
	}
}

func TestClmMemberBuilder_EntitlementsAndGrants_AreNoop(t *testing.T) {
	// clm_member is a pure principal: it holds no entitlements of its own, and any
	// membership/role grants it's part of are emitted from the other side (clm_group,
	// clm_folder) per this project's own "emit from whichever side is cheapest" pattern.
	_, c := clmtest.NewServer(t)
	b := newClmMemberBuilder(c)
	ctx := context.Background()

	memberResource, err := rs.NewResource("Alice", clmMemberResourceType, "member-alice")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	if ents, res, err := b.Entitlements(ctx, memberResource, rs.SyncOpAttrs{}); err != nil || ents != nil || res != nil {
		t.Errorf("expected Entitlements to return (nil, nil, nil), got (%v, %v, %v)", ents, res, err)
	}
	if grants, res, err := b.Grants(ctx, memberResource, rs.SyncOpAttrs{}); err != nil || grants != nil || res != nil {
		t.Errorf("expected Grants to return (nil, nil, nil), got (%v, %v, %v)", grants, res, err)
	}
}
