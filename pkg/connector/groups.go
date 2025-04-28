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

type groupsClientInterface interface {
	GetGroups(ctx context.Context) ([]client.Group, annotations.Annotations, error)
	GetGroupUsers(ctx context.Context, groupID string) ([]client.User, annotations.Annotations, error)
}

type groupBuilder struct {
	resourceType *v2.ResourceType
	client       groupsClientInterface
}

func (g *groupBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return groupResourceType
}

func (g *groupBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	groups, annos, err := g.client.GetGroups(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("docusign-connector: failed to list groups: %w", err)
	}

	resources := make([]*v2.Resource, 0, len(groups))
	for _, group := range groups {
		groupResource, err := parseIntoGroupResource(&group)
		if err != nil {
			return nil, "", nil, fmt.Errorf("docusign-connector: failed to parse group: %w", err)
		}
		resources = append(resources, groupResource)
	}

	return resources, "", annos, nil
}

func (g *groupBuilder) Entitlements(ctx context.Context, res *v2.Resource, pToken *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	ent := entitlement.NewAssignmentEntitlement(
		res,
		"member",
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(fmt.Sprintf("Member of %s", res.DisplayName)),
		entitlement.WithDescription(fmt.Sprintf("Member of %s group", res.DisplayName)),
	)
	return []*v2.Entitlement{ent}, "", nil, nil
}

func (g *groupBuilder) Grants(ctx context.Context, res *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	groupUsers, annos, err := g.client.GetGroupUsers(ctx, res.Id.Resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("docusign-connector: failed to get group users for %s: %w", res.Id.Resource, err)
	}

	grants := make([]*v2.Grant, 0, len(groupUsers))
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
			return nil, "", nil, fmt.Errorf("docusign-connector: failed to create user resource: %w", err)
		}

		grants = append(grants, grant.NewGrant(
			res,
			"member",
			userResource.Id,
			grant.WithGrantMetadata(map[string]interface{}{
				"group_id":   res.Id.Resource,
				"group_name": res.DisplayName,
				"user_id":    user.UserId,
				"username":   user.UserName,
			}),
		))
	}

	return grants, "", annos, nil
}

func newGroupBuilder(client *client.Client) *groupBuilder {
	return &groupBuilder{
		resourceType: groupResourceType,
		client:       client,
	}
}

func parseIntoGroupResource(group *client.Group) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_name":  group.GroupName,
		"group_type":  group.GroupType,
		"users_count": group.UsersCount,
	}

	return resource.NewGroupResource(
		group.GroupName,
		groupResourceType,
		group.GroupId,
		[]resource.GroupTraitOption{
			resource.WithGroupProfile(profile),
		},
	)
}
