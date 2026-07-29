package connector

import (
	"context"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

// clmRoleBuilder syncs the 5 fixed CLM account-level roles (client.ClmRoles). Not
// backed by an API call — see resource_types.go for why this resource type exists.
type clmRoleBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
	includeClm   bool
}

func (b *clmRoleBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmRoleResourceType
}

// List returns the fixed set of CLM roles. No pagination needed — the set is small
// and hardcoded, not fetched from the API. Still gated on include-clm even though this
// makes no client call itself: without it, every eSignature-only account would sync 5
// "CLM Role" resources that correspond to nothing in their account and can never be
// granted (Grants is a no-op below) — cosmetic, but needless noise in the C1 UI.
func (b *clmRoleBuilder) List(_ context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if !b.includeClm {
		return nil, &rs.SyncOpResults{}, nil
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

func newClmRoleBuilder(c *client.Client, includeClm bool) *clmRoleBuilder {
	return &clmRoleBuilder{
		resourceType: clmRoleResourceType,
		client:       c,
		includeClm:   includeClm,
	}
}
