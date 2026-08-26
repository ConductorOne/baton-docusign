package connector

import (
	"context"
	"strings"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// TestClmMemberBuilder_List_StampsWorkflowQueueChildResourceType confirms
// parseIntoClmMemberResource stamps the ChildResourceType annotation on every synced
// clm_member RESOURCE INSTANCE, not just declared on clmMemberResourceType itself — the
// SDK's child-resource scheduling (childResourceTypeIDs, pkg/sync/syncer.go) reads it off
// each instance, so a resource missing this annotation would silently never get its
// clm_workflow_queue.List() call triggered even though the type declaration looks correct.
func TestClmMemberBuilder_List_StampsWorkflowQueueChildResourceType(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmMemberBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) == 0 {
		t.Fatal("expected at least one clm_member resource")
	}
	for _, r := range resources {
		annos := annotations.Annotations(r.Annotations)
		var child v2.ChildResourceType
		ok, err := annos.Pick(&child)
		if err != nil {
			t.Fatalf("resource %s: Pick(ChildResourceType): %v", r.Id.Resource, err)
		}
		if !ok {
			t.Errorf("resource %s: expected a ChildResourceType annotation", r.Id.Resource)
			continue
		}
		if child.ResourceTypeId != clmWorkflowQueueResourceType.Id {
			t.Errorf("resource %s: expected ChildResourceType.ResourceTypeId %q, got %q", r.Id.Resource, clmWorkflowQueueResourceType.Id, child.ResourceTypeId)
		}
	}
}

func TestClmMemberBuilder_List_FailsWhenClmUnavailable(t *testing.T) {
	// clm_member (like every OptInRequired CLM/signing_group resource type) only ever
	// syncs once a customer has explicitly opted it in, and C1's opt-in toggle has no
	// upstream check against DocuSign — so an account/token that can't use CLM at that
	// point is a real misconfiguration, not an expected state. List() must fail loudly
	// rather than silently succeed with zero resources — see clm_roles.go's doc comment.
	s, _ := clmtest.NewServer(t)
	badClient := s.NewClientWithToken("wrong-token")
	b := newClmMemberBuilder(badClient)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err == nil {
		t.Fatal("expected List to fail when CLM is unavailable, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

func TestClmMemberBuilder_Entitlements_IsNoop(t *testing.T) {
	// clm_member is a pure principal: it holds no entitlements of its own.
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
}

// TestClmMemberBuilder_Grants_EmitsWorkflowQueueMembership is the core regression test
// for the new design: Grants() moved here from clmWorkflowQueueBuilder (see
// clm_workflow_queues.go's doc) since CLM only exposes workflow-queue membership per
// member. member-bob (clmtest/seed.go) belongs to both seeded queues (Onboarding,
// Escalations) — this confirms one grant per queue, with the queue as the
// entitlement-resource side and the member as the principal.
func TestClmMemberBuilder_Grants_EmitsWorkflowQueueMembership(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmMemberBuilder(c)
	ctx := context.Background()

	memberResource, err := rs.NewResource("Bob", clmMemberResourceType, "member-bob")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	grants, res, err := b.Grants(ctx, memberResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil SyncOpResults")
	}
	if len(grants) != 2 {
		t.Fatalf("expected 2 grants for member-bob's workflow queue membership, got %d: %+v", len(grants), grants)
	}

	gotQueueIDs := make(map[string]bool, len(grants))
	for _, g := range grants {
		if g.Entitlement.Resource.Id.ResourceType != clmWorkflowQueueResourceType.Id {
			t.Errorf("expected the entitlement-holder to be a clm_workflow_queue, got %s", g.Entitlement.Resource.Id.ResourceType)
		}
		wantEntID := clmWorkflowQueueResourceType.Id + ":" + g.Entitlement.Resource.Id.Resource + ":" + entitlementClmWorkflowQueueMember
		if g.Entitlement.Id != wantEntID {
			t.Errorf("expected entitlement id %q, got %q", wantEntID, g.Entitlement.Id)
		}
		if g.Principal.Id.ResourceType != clmMemberResourceType.Id || g.Principal.Id.Resource != "member-bob" {
			t.Errorf("expected principal member-bob, got %s:%s", g.Principal.Id.ResourceType, g.Principal.Id.Resource)
		}
		gotQueueIDs[g.Entitlement.Resource.Id.Resource] = true
	}
	for _, want := range []string{"queue-onboarding", "queue-escalations"} {
		if !gotQueueIDs[want] {
			t.Errorf("expected a grant for queue %q, got %v", want, gotQueueIDs)
		}
	}
}

// TestClmMemberBuilder_Grants_NoQueues confirms a member with zero workflow-queue
// memberships (member-carol, clmtest/seed.go) returns an empty, non-nil grant slice
// rather than an error.
func TestClmMemberBuilder_Grants_NoQueues(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmMemberBuilder(c)
	ctx := context.Background()

	memberResource, err := rs.NewResource("Carol", clmMemberResourceType, "member-carol")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	grants, _, err := b.Grants(ctx, memberResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected zero grants for member-carol, got %d: %+v", len(grants), grants)
	}
}

// TestClmMemberBuilder_Grants_PropagatesClientError confirms a GetMemberWorkflowQueues
// failure propagates — wrapped with the baton-docusign: prefix, with the underlying
// gRPC code still reachable through the wrap — rather than being swallowed.
func TestClmMemberBuilder_Grants_PropagatesClientError(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	b := newClmMemberBuilder(c)
	ctx := context.Background()

	memberResource, err := rs.NewResource("Alice", clmMemberResourceType, "member-alice")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	grants, _, err := b.Grants(ctx, memberResource, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected Grants to propagate the underlying client error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "baton-docusign:") {
		t.Errorf("expected error wrapped with the baton-docusign: prefix, got: %v", err)
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected the underlying PermissionDenied code to still be reachable through the wrap, got code %s (err: %v)", status.Code(err), err)
	}
	if grants != nil {
		t.Errorf("expected nil grants on a hard failure, got %+v", grants)
	}
}

// TestClmMemberBuilder_Grants_SkipsQueueWithEmptyHref confirms a queue with an empty
// Href-derived ID is skipped rather than emitted as a malformed grant with an empty
// entitlement-resource ID — mirrors clm_workflow_queues_test.go's equivalent List()
// case, since Grants() here shares the identical per-queue skip check.
func TestClmMemberBuilder_Grants_SkipsQueueWithEmptyHref(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.AddMemberWorkflowQueueWithEmptyHref("member-no-href-queue")
	b := newClmMemberBuilder(c)
	ctx := context.Background()

	memberResource, err := rs.NewResource("No Href Queue Member", clmMemberResourceType, "member-no-href-queue")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	grants, _, err := b.Grants(ctx, memberResource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected the empty-Href queue to be skipped, got %d grants: %+v", len(grants), grants)
	}
}
