package connector

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
)

// Seeded members that hold a role; member-no-role has none and gets no grant.
var seededMemberRoles = map[string]string{
	"member-alice":             "FullSubscriber",
	"member-bob":               "FullSubscriber",
	"member-carol":             "LimitedSubscriber",
	"member-dave":              "Guest",
	"member-eve":               "SuperAdministrator",
	"member-clm-admin":         "CLMAdmin",
	"member-clm-user":          "CLMUser",
	"member-contract-reviewer": "ContractReviewer",
	"member-frank":             "FullSubscriber",
}

var shippedRoles = []string{"FullSubscriber", "Guest", "LimitedSubscriber", "SuperAdministrator", "UserAdministrator"}

func resourceIDs(resources []*v2.Resource) []string {
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource.Id.Resource)
	}
	return ids
}

// listClmRolePages runs clm_role List over every page and returns the role IDs of each page.
func listClmRolePages(t *testing.T, b *clmRoleBuilder, store *fakeSessionStore, pageSize int) [][]string {
	t.Helper()

	var pages [][]string
	pageToken := ""
	for i := 0; i < 20; i++ {
		resources, res, err := b.List(context.Background(), nil, rs.SyncOpAttrs{
			Session:   store,
			PageToken: pagination.Token{Size: pageSize, Token: pageToken},
		})
		if err != nil {
			t.Fatalf("List page %d: %v", i, err)
		}
		pages = append(pages, resourceIDs(resources))
		if res.NextPageToken == "" {
			return pages
		}
		pageToken = res.NextPageToken
	}
	t.Fatal("List did not reach the last page within 20 pages")
	return nil
}

func TestClmRoleBuilder_List(t *testing.T) {
	_, c := clmtest.NewServer(t)

	pages := listClmRolePages(t, newClmRoleBuilder(c, true), newFakeSessionStore(), 3)

	wantPages := [][]string{
		shippedRoles, // alice, bob and carol hold shipped roles only
		{"CLMAdmin", "Guest", "SuperAdministrator"},
		{"CLMUser", "ContractReviewer"}, // member-no-role holds none
		{"FullSubscriber"},
	}
	if !reflect.DeepEqual(pages, wantPages) {
		t.Errorf("expected pages %v, got %v", wantPages, pages)
	}
}

func TestClmRoleBuilder_List_WithoutMembersEmitsShippedRoles(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ClearMembers()

	pages := listClmRolePages(t, newClmRoleBuilder(c, true), newFakeSessionStore(), 100)

	if want := [][]string{shippedRoles}; !reflect.DeepEqual(pages, want) {
		t.Errorf("expected only the shipped roles %v, got %v", want, pages)
	}
}

func TestClmRoleBuilder_List_SessionStore(t *testing.T) {
	_, c := clmtest.NewServer(t)

	store := newFakeSessionStore()
	listClmRolePages(t, newClmRoleBuilder(c, true), store, 3)
	wantRoles := []string{"CLMAdmin", "CLMUser", "ContractReviewer", "FullSubscriber", "Guest", "LimitedSubscriber", "SuperAdministrator", "UserAdministrator"}
	if got := store.keysWithPrefix(clmRoleSessionPrefix); !reflect.DeepEqual(got, wantRoles) {
		t.Errorf("expected the store to hold %v, got %v", wantRoles, got)
	}
}

func TestClmRoleBuilder_PropagatesErrors(t *testing.T) {
	ctx := context.Background()
	pageToken := pagination.Token{Size: 100}

	t.Run("List: members list failure", func(t *testing.T) {
		srv, c := clmtest.NewServer(t)
		srv.ForceListMembersStatus(http.StatusInternalServerError)

		if _, _, err := newClmRoleBuilder(c, true).List(ctx, nil, rs.SyncOpAttrs{Session: newFakeSessionStore(), PageToken: pageToken}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("List: session write failure", func(t *testing.T) {
		_, c := clmtest.NewServer(t)
		store := newFakeSessionStore()
		store.setManyErr = errors.New("session store unavailable")

		if _, _, err := newClmRoleBuilder(c, true).List(ctx, nil, rs.SyncOpAttrs{Session: store, PageToken: pageToken}); !errors.Is(err, store.setManyErr) {
			t.Fatalf("expected the session write error, got %v", err)
		}
	})

	t.Run("GrantsForResourceType: members list failure", func(t *testing.T) {
		srv, c := clmtest.NewServer(t)
		srv.ForceListMembersStatus(http.StatusInternalServerError)

		if _, _, err := newClmRoleBuilder(c, true).GrantsForResourceType(ctx, clmRoleResourceType.Id, rs.SyncOpAttrs{Session: newFakeSessionStore(), PageToken: pageToken}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("GrantsForResourceType: session read failure", func(t *testing.T) {
		_, c := clmtest.NewServer(t)
		store := newFakeSessionStore()
		store.getErr = errors.New("session store unavailable")

		if _, _, err := newClmRoleBuilder(c, true).GrantsForResourceType(ctx, clmRoleResourceType.Id, rs.SyncOpAttrs{Session: store, PageToken: pageToken}); !errors.Is(err, store.getErr) {
			t.Fatalf("expected the session read error, got %v", err)
		}
	})

	t.Run("GrantsForResourceType: synced roles read failure", func(t *testing.T) {
		_, c := clmtest.NewServer(t)
		store := newFakeSessionStore()
		store.getManyErr = errors.New("session store unavailable")

		if _, _, err := newClmRoleBuilder(c, true).GrantsForResourceType(ctx, clmRoleResourceType.Id, rs.SyncOpAttrs{Session: store, PageToken: pageToken}); !errors.Is(err, store.getManyErr) {
			t.Fatalf("expected the synced roles read error, got %v", err)
		}
	})
}

func TestClmRoleDisplayName(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"CLMAdmin", "CLM Admin"},
		{"CLMUser", "CLM User"},
		{"FullSubscriber", "Full Subscriber"},
		{"Guest", "Guest"},
		{"HRAdmin2", "HR Admin2"},
		{"CLM Admin", "CLM Admin"},
		{"", ""},
	}
	for _, testCase := range tests {
		if got := clmRoleDisplayName(testCase.role); got != testCase.want {
			t.Errorf("clmRoleDisplayName(%q) = %q, want %q", testCase.role, got, testCase.want)
		}
	}
}

func TestClmRoleBuilder_ResourceType_Annotations(t *testing.T) {
	annos := annotations.Annotations(newClmRoleBuilder(nil, true).ResourceType(context.Background()).Annotations)
	if !annos.Contains(&v2.OptInRequired{}) || !annos.Contains(&v2.SkipEntitlements{}) || !annos.Contains(&v2.TypeScopedGrants{}) {
		t.Errorf("expected OptInRequired, SkipEntitlements and TypeScopedGrants annotations, got %v", annos)
	}
}

func TestClmRoleBuilder_StaticEntitlements(t *testing.T) {
	ents, _, err := newClmRoleBuilder(nil, true).StaticEntitlements(context.Background(), rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("StaticEntitlements: %v", err)
	}
	if len(ents) != 1 || ents[0].Slug != clmRoleAssignedTag || ents[0].Purpose != v2.Entitlement_PURPOSE_VALUE_ASSIGNMENT {
		t.Fatalf("expected one %q assignment entitlement, got %v", clmRoleAssignedTag, ents)
	}
	if len(ents[0].GrantableTo) != 0 {
		t.Errorf("expected no GrantableTo since roles are not provisionable, got %v", ents[0].GrantableTo)
	}
}

// listClmRoleGrants runs clm_role GrantsForResourceType over every page and returns all grants.
func listClmRoleGrants(t *testing.T, b *clmRoleBuilder, store *fakeSessionStore, pageSize int) []*v2.Grant {
	t.Helper()

	var grants []*v2.Grant
	pageToken := ""
	for i := 0; i < 20; i++ {
		pageGrants, res, err := b.GrantsForResourceType(context.Background(), clmRoleResourceType.Id, rs.SyncOpAttrs{
			Session:   store,
			PageToken: pagination.Token{Size: pageSize, Token: pageToken},
		})
		if err != nil {
			t.Fatalf("GrantsForResourceType page %d: %v", i, err)
		}
		grants = append(grants, pageGrants...)
		if res.NextPageToken == "" {
			return grants
		}
		pageToken = res.NextPageToken
	}
	t.Fatal("GrantsForResourceType did not reach the last page within 20 pages")
	return nil
}

func TestClmRoleBuilder_GrantsForResourceType(t *testing.T) {
	_, c := clmtest.NewServer(t)

	// No member pages are stored, so every page is read from CLM.
	store := newFakeSessionStore()
	listClmRolePages(t, newClmRoleBuilder(c, true), store, 3)
	grants := listClmRoleGrants(t, newClmRoleBuilder(c, true), store, 3)

	gotRoles := map[string]string{}
	for _, roleGrant := range grants {
		memberID := roleGrant.Principal.Id.Resource
		role := roleGrant.Entitlement.Resource.Id.Resource
		if roleGrant.Principal.Id.ResourceType != clmMemberResourceType.Id {
			t.Errorf("member %s: expected principal type %q, got %q", memberID, clmMemberResourceType.Id, roleGrant.Principal.Id.ResourceType)
		}
		if wantEntitlementID := "clm_role:" + role + ":" + clmRoleAssignedTag; roleGrant.Entitlement.Id != wantEntitlementID {
			t.Errorf("member %s: expected entitlement %q, got %q", memberID, wantEntitlementID, roleGrant.Entitlement.Id)
		}
		grantAnnos := annotations.Annotations(roleGrant.Annotations)
		if !grantAnnos.Contains(&v2.GrantImmutable{}) {
			t.Errorf("member %s: expected a GrantImmutable annotation", memberID)
		}
		gotRoles[memberID] = role
	}

	if !reflect.DeepEqual(gotRoles, seededMemberRoles) {
		t.Errorf("expected member-to-role grants %v, got %v", seededMemberRoles, gotRoles)
	}
}

func TestClmRoleBuilder_GrantsForResourceType_ReadsStoredMemberPages(t *testing.T) {
	// Without this, the uhttp GET cache serves the earlier pages and the forced 500 never hits.
	t.Setenv("BATON_DISABLE_HTTP_CACHE", "true")
	srv, c := clmtest.NewServer(t)
	store := newFakeSessionStore()

	memberBuilder := newClmMemberBuilder(c, false, true)
	pageToken := ""
	for i := 0; i < 20; i++ {
		_, res, err := memberBuilder.List(context.Background(), nil, rs.SyncOpAttrs{Session: store, PageToken: pagination.Token{Size: 3, Token: pageToken}})
		if err != nil {
			t.Fatalf("clm_member List page %d: %v", i, err)
		}
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}
	listClmRolePages(t, newClmRoleBuilder(c, true), store, 3)

	// Any page read from CLM now fails, so every grant must come from the stored pages.
	srv.ForceListMembersStatus(http.StatusInternalServerError)
	grants := listClmRoleGrants(t, newClmRoleBuilder(c, true), store, 3)

	gotRoles := map[string]string{}
	for _, roleGrant := range grants {
		gotRoles[roleGrant.Principal.Id.Resource] = roleGrant.Entitlement.Resource.Id.Resource
	}
	if !reflect.DeepEqual(gotRoles, seededMemberRoles) {
		t.Errorf("expected member-to-role grants %v, got %v", seededMemberRoles, gotRoles)
	}
}

func TestClmRoleBuilder_GrantsForResourceType_SkipsRolesListDidNotEmit(t *testing.T) {
	_, c := clmtest.NewServer(t)
	store := newFakeSessionStore()
	listClmRolePages(t, newClmRoleBuilder(c, true), store, 3)
	// A role the member took after clm_role List ran has no key.
	if err := store.Delete(context.Background(), "CLMAdmin", sessions.WithPrefix(clmRoleSessionPrefix)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	grants := listClmRoleGrants(t, newClmRoleBuilder(c, true), store, 3)

	gotRoles := map[string]string{}
	for _, roleGrant := range grants {
		gotRoles[roleGrant.Principal.Id.Resource] = roleGrant.Entitlement.Resource.Id.Resource
	}
	wantRoles := maps.Clone(seededMemberRoles)
	delete(wantRoles, "member-clm-admin")
	if !reflect.DeepEqual(gotRoles, wantRoles) {
		t.Errorf("expected member-to-role grants %v, got %v", wantRoles, gotRoles)
	}
}

func TestClmRoleBuilder_GrantsForResourceType_SkippedWithoutMembers(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceListMembersStatus(http.StatusInternalServerError)

	grants, res, err := newClmRoleBuilder(c, false).GrantsForResourceType(context.Background(), clmRoleResourceType.Id, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 100}})
	if err != nil {
		t.Fatalf("expected no members API call when clm_member is not synced, got error: %v", err)
	}
	if len(grants) != 0 || res.NextPageToken != "" {
		t.Errorf("expected no grants and no next page, got %d grants and token %q", len(grants), res.NextPageToken)
	}
}
