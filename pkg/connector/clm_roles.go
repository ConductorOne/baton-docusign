package connector

import (
	"context"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

// clmRoleBuilder syncs the 5 fixed CLM account-level roles (client.ClmRoles). Not
// backed by an API call — see resource_types.go for why this resource type exists.
// CLM availability is checked once, up front, by Connector.Validate() rather than here
// — see that method's doc for why centralizing it there is better than every opted-in
// CLM builder repeating the same check on its own first page. Unlike every other CLM
// builder, this one never calls the API at all, so it holds no *client.Client.
type clmRoleBuilder struct {
	resourceType *v2.ResourceType
}

func (b *clmRoleBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmRoleResourceType
}

// List returns the fixed set of CLM roles. No pagination needed — the set is small and
// hardcoded, not fetched from the API. CLM availability was already confirmed once, up
// front, by Connector.Validate() before any builder's List() runs — see that method's
// doc — so there's no error path here beyond rs.NewRoleResource construction failing.
func (b *clmRoleBuilder) List(_ context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
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

func newClmRoleBuilder() *clmRoleBuilder {
	return &clmRoleBuilder{
		resourceType: clmRoleResourceType,
	}
}
