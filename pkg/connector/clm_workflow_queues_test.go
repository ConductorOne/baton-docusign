package connector

import (
	"context"
	"strings"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestClmWorkflowQueueBuilder_List_ReturnsMemberWorkflowQueues is the core regression
// test for this builder's redesign: clm_workflow_queue is now clmMemberResourceType's
// ChildResourceType (see resource_types.go), so List() is driven per-member by the SDK's
// own child-resource scheduling instead of an independent, hand-rolled member scan.
// member-bob (clmtest/seed.go) belongs to both seeded queues (Onboarding, Escalations) —
// this confirms List() returns exactly that member's queues by stable queue ID.
func TestClmWorkflowQueueBuilder_List_ReturnsMemberWorkflowQueues(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	parentResourceID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: "member-bob"}

	resources, res, err := b.List(ctx, parentResourceID, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil SyncOpResults")
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 workflow queues for member-bob, got %d: %+v", len(resources), resources)
	}

	gotIDs := make(map[string]bool, len(resources))
	for _, r := range resources {
		if r.Id.ResourceType != clmWorkflowQueueResourceType.Id {
			t.Errorf("expected resource type %q, got %q", clmWorkflowQueueResourceType.Id, r.Id.ResourceType)
		}
		if r.ParentResourceId != nil {
			t.Errorf("expected no ParentResourceId (shared queues have no canonical parent), got %+v", r.ParentResourceId)
		}
		gotIDs[r.Id.Resource] = true
	}
	for _, want := range []string{"queue-onboarding", "queue-escalations"} {
		if !gotIDs[want] {
			t.Errorf("expected queue %q among member-bob's workflow queues, got %v", want, gotIDs)
		}
	}
}

// TestClmWorkflowQueueBuilder_List_SingleQueueMember is a narrower complement to
// ReturnsMemberWorkflowQueues above: member-alice belongs to exactly one seeded queue
// (Onboarding), confirming List() doesn't leak another member's queues onto this one.
func TestClmWorkflowQueueBuilder_List_SingleQueueMember(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	parentResourceID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: "member-alice"}
	resources, _, err := b.List(ctx, parentResourceID, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 1 || resources[0].Id.Resource != "queue-onboarding" {
		t.Fatalf("expected exactly [queue-onboarding] for member-alice, got %+v", resources)
	}
}

// TestClmWorkflowQueueBuilder_List_NoQueues confirms a member with zero workflow-queue
// memberships (member-carol, clmtest/seed.go) produces an empty, non-error result rather
// than List() treating "no queues" as a fault.
func TestClmWorkflowQueueBuilder_List_NoQueues(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	parentResourceID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: "member-carol"}
	resources, _, err := b.List(ctx, parentResourceID, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected zero workflow queues for member-carol, got %d: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_List_NilParentResourceID pins List()'s no-op for the SDK's
// mandatory unparented top-level call (one per registered resource type). Child discovery
// happens via parented calls driven by clmMemberResourceType's ChildResourceType annotation.
func TestClmWorkflowQueueBuilder_List_NilParentResourceID(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("expected nil error for unparented top-level List(), got: %v", err)
	}
	if resources != nil {
		t.Errorf("expected nil resources, got %+v", resources)
	}
	if res != nil {
		t.Errorf("expected nil SyncOpResults, got %+v", res)
	}
}

// TestClmWorkflowQueueBuilder_List_PropagatesClientError confirms a
// GetMemberWorkflowQueues failure propagates — wrapped with the baton-docusign: prefix,
// with the underlying gRPC code still reachable through the wrap — rather than being
// swallowed or downgraded to an empty result. Unlike the old session-store design, this
// builder has no error-tolerance logic of its own to bypass here.
func TestClmWorkflowQueueBuilder_List_PropagatesClientError(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.ForceMemberWorkflowQueuesStatus("member-alice", 403)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	parentResourceID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: "member-alice"}
	resources, _, err := b.List(ctx, parentResourceID, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected List to propagate the underlying client error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "baton-docusign:") {
		t.Errorf("expected error wrapped with the baton-docusign: prefix, got: %v", err)
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected the underlying PermissionDenied code to still be reachable through the wrap, got code %s (err: %v)", status.Code(err), err)
	}
	if resources != nil {
		t.Errorf("expected nil resources on a hard failure, got %+v", resources)
	}
}

// TestClmWorkflowQueueBuilder_List_SkipsQueueWithEmptyHref confirms a queue with an
// empty Href-derived ID is skipped, not turned into a malformed resource with an empty
// native ID — mirrors clm_folders.go/clm_groups.go's established pattern of skipping
// rather than erroring on an unusable ID (see e.g. clmFolderBuilder.Grants' skip of
// unmapped AccessType entries and AddMemberWithoutHref's equivalent member-level case).
func TestClmWorkflowQueueBuilder_List_SkipsQueueWithEmptyHref(t *testing.T) {
	srv, c := clmtest.NewServer(t)
	srv.AddMemberWorkflowQueueWithEmptyHref("member-no-href-queue")
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	parentResourceID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: "member-no-href-queue"}
	resources, _, err := b.List(ctx, parentResourceID, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected the empty-Href queue to be skipped, got %d resources: %+v", len(resources), resources)
	}
}

// TestClmWorkflowQueueBuilder_Grants_IsNoop pins that this builder's Grants() is a
// deliberate no-op: membership grants moved to clmMemberBuilder.Grants() (clm_members.go)
// since CLM only exposes workflow-queue membership per member. A future reader might
// otherwise expect grants to still come from here.
func TestClmWorkflowQueueBuilder_Grants_IsNoop(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	queueResource, err := rs.NewGroupResource("Onboarding", clmWorkflowQueueResourceType, "queue-onboarding", nil)
	if err != nil {
		t.Fatalf("NewGroupResource: %v", err)
	}

	grants, res, err := b.Grants(ctx, queueResource, rs.SyncOpAttrs{})
	if err != nil || grants != nil || res != nil {
		t.Errorf("expected Grants to return (nil, nil, nil), got (%v, %v, %v)", grants, res, err)
	}
}

// TestClmWorkflowQueueBuilder_StaticEntitlements confirms every CLM workflow queue
// shares the single "member" entitlement, grantable only to clm_member.
func TestClmWorkflowQueueBuilder_StaticEntitlements(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	ents, res, err := b.StaticEntitlements(ctx, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("StaticEntitlements: %v", err)
	}
	if res != nil {
		t.Errorf("expected nil SyncOpResults, got %+v", res)
	}
	if len(ents) != 1 || ents[0].Slug != entitlementClmWorkflowQueueMember {
		t.Fatalf("expected a single %q entitlement, got %+v", entitlementClmWorkflowQueueMember, ents)
	}
	if len(ents[0].GrantableTo) != 1 || ents[0].GrantableTo[0].Id != clmMemberResourceType.Id {
		t.Errorf("expected the entitlement to be grantable only to clm_member, got %+v", ents[0].GrantableTo)
	}
}

// TestClmWorkflowQueueBuilder_Entitlements_IsNoop confirms Entitlements() returns nil —
// the SDK doesn't call it once StaticEntitlementSyncerV2 is implemented, but this pins
// the contract directly the same way clm_groups_test.go/clm_folders_test.go do for their
// own StaticEntitlementSyncerV2 builders.
func TestClmWorkflowQueueBuilder_Entitlements_IsNoop(t *testing.T) {
	_, c := clmtest.NewServer(t)
	b := newClmWorkflowQueueBuilder(c)
	ctx := context.Background()

	queueResource, err := rs.NewGroupResource("Onboarding", clmWorkflowQueueResourceType, "queue-onboarding", nil)
	if err != nil {
		t.Fatalf("NewGroupResource: %v", err)
	}

	ents, res, err := b.Entitlements(ctx, queueResource, rs.SyncOpAttrs{})
	if err != nil || ents != nil || res != nil {
		t.Errorf("expected Entitlements to return (nil, nil, nil), got (%v, %v, %v)", ents, res, err)
	}
}
