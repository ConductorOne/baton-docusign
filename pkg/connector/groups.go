package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

type groupBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

// ResourceType returns the type of resource this builder is responsible for.
func (g *groupBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return groupResourceType
}

// List retrieves the list of groups from the DocuSign API and converts them into C1 resources.
func (g *groupBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var resources []*v2.Resource
	annos := annotations.Annotations{}

	groups, newAnnos, err := g.client.GetGroups(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, a := range newAnnos {
		annos.Append(a)
	}

	for _, group := range groups {
		groupCopy := group
		groupResource, err := parseIntoGroupResource(&groupCopy)
		if err != nil {
			return nil, "", nil, err
		}
		resources = append(resources, groupResource)
	}

	return resources, "", annos, nil
}

// Entitlements returns the entitlements (permissions) associated with a group resource.
func (g *groupBuilder) Entitlements(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	e := entitlement.NewAssignmentEntitlement(
		resource,
		"member",
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(fmt.Sprintf("Member of %s", resource.DisplayName)),
		entitlement.WithDescription(fmt.Sprintf("Member of %s group", resource.DisplayName)),
	)
	return []*v2.Entitlement{e}, "", nil, nil
}

// Grants returns the list of grants (assignments of entitlements) for users in a group.
func (g *groupBuilder) Grants(ctx context.Context, parentResource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant
	annos := annotations.Annotations{}

	groupUsers, newAnnos, err := g.client.GetGroupUsers(ctx, parentResource.Id.Resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("error fetching group users: %w", err)
	}
	for _, a := range newAnnos {
		annos.Append(a)
	}

	for _, user := range groupUsers {
		userResource, err := resource.NewUserResource(
			user.UserName,
			userResourceType,
			user.UserId,
			[]resource.UserTraitOption{
				resource.WithEmail(user.Email, true),
				resource.WithUserProfile(map[string]interface{}{
					"status": user.UserStatus,
				}),
			},
		)
		if err != nil {
			return nil, "", nil, err
		}

		grant := grant.NewGrant(
			parentResource,
			"member",
			userResource.Id,
			grant.WithGrantMetadata(map[string]interface{}{
				"group_id":   parentResource.Id.Resource,
				"group_name": parentResource.DisplayName,
				"user_id":    user.UserId,
				"username":   user.UserName,
			}),
		)
		grants = append(grants, grant)
	}

	return grants, "", annos, nil
}

// newGroupBuilder initializes a new groupBuilder instance.
func newGroupBuilder(client *client.Client) *groupBuilder {
	return &groupBuilder{
		resourceType: groupResourceType,
		client:       client,
	}
}

// parseIntoGroupResource converts a DocuSign Group object into a C1 Resource object.
func parseIntoGroupResource(group *client.Group) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"groupName":   group.GroupName,
		"groupType":   group.GroupType,
		"users_count": group.UsersCount,
	}

	groupTraits := []resource.GroupTraitOption{
		resource.WithGroupProfile(profile),
	}

	return resource.NewGroupResource(
		group.GroupName,
		groupResourceType,
		group.GroupId,
		groupTraits,
	)
}
