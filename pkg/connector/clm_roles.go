package connector

import (
	"context"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

// clmRoleBuilder syncs the 5 fixed CLM account-level roles (client.ClmRoles). The role
// set itself isn't backed by an API call — see resource_types.go for why this resource
// type exists — but List() still checks CLM availability via EnsureClmReady before
// emitting it, the same discovery check every other CLM builder's real API call runs
// internally.
type clmRoleBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (b *clmRoleBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmRoleResourceType
}

// List returns the fixed set of CLM roles, gated on CLM being available for this
// account. No pagination needed — the set is small and hardcoded, not fetched from the
// API — so the availability check always runs (there's no first-page-only gate to
// apply, unlike the paginated CLM builders).
//
// clm_role is OptInRequired (resource_types.go), so this List() only ever runs once a
// customer has explicitly enabled it in their sync config — the C1 platform's toggle for
// that has no upstream validation against DocuSign, so a customer can opt in without
// actually having a CLM subscription. When that happens, EnsureClmReady failing here is
// a real misconfiguration, not a transient/expected condition: fail loudly so it's
// visible, rather than silently syncing zero roles indefinitely.
func (b *clmRoleBuilder) List(ctx context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if err := b.client.EnsureClmReady(ctx); err != nil {
		return nil, nil, err
	}

	var resources []*v2.Resource
	for _, role := range client.ClmRoles {
		roleResource, err := rs.NewRoleResource(
			role.Name,
			clmRoleResourceType,
			role.Name,
			nil,
		)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, roleResource)
	}
	return resources, &rs.SyncOpResults{}, nil
}

// Entitlements: clm_role is a pure grant target (like a user/group), not itself an
// entitlement-holder.
func (b *clmRoleBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants: nothing grants access to a CLM role itself; roles are a principal-like
// grant target for folder security, not a resource with its own membership.
func (b *clmRoleBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func newClmRoleBuilder(c *client.Client) *clmRoleBuilder {
	return &clmRoleBuilder{
		resourceType: clmRoleResourceType,
		client:       c,
	}
}
