package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

// Entitlement value representing group membership.
const entitlementGroupMember = "member"

// groupBuilder implements resource listing, entitlements, and grants for DocuSign groups.
type groupBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

// ResourceType returns the Baton resource type handled by this builder.
func (g *groupBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return groupResourceType
}

// List fetches groups from the API, converts them to Baton resources, and returns pagination info.
func (g *groupBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var resources []*v2.Resource

	annos := annotations.Annotations{}
	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: groupResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	groups, nextPageToken, newAnnos, err := g.client.GetGroups(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, nil, err
	}

	for _, annon := range newAnnos {
		annos.Append(annon)
	}

	for _, group := range groups {
		groupResource, err := parseIntoGroupResource(&group)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, groupResource)
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}

	return resources, &rs.SyncOpResults{
		Annotations:   annos,
		NextPageToken: outToken,
	}, nil
}

// Entitlements returns a "member" entitlement for each group, grantable to users.
func (g *groupBuilder) Entitlements(ctx context.Context, groupResource *v2.Resource, attr rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	annos := annotations.Annotations{}
	ent := entitlement.NewAssignmentEntitlement(
		groupResource,
		entitlementGroupMember,
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(fmt.Sprintf("Member of %s", groupResource.DisplayName)),
		entitlement.WithDescription(fmt.Sprintf("Member of %s group", groupResource.DisplayName)),
	)
	return []*v2.Entitlement{ent}, &rs.SyncOpResults{Annotations: annos}, nil
}

// Grants fetches users in the group and returns grants for the "member" entitlement.
func (g *groupBuilder) Grants(ctx context.Context, groupResource *v2.Resource, attr rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, nil, err
	}
	groupUsers, nextPageToken, annos, err := g.client.GetGroupUsers(ctx, groupResource.Id.Resource, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get group users for %s: %w", groupResource.Id.Resource, err)
	}
	grants := make([]*v2.Grant, 0, len(groupUsers))
	for _, user := range groupUsers {
		userResource := &v2.Resource{
			Id: &v2.ResourceId{
				ResourceType: userResourceType.Id,
				Resource:     user.UserId,
			},
		}
		grants = append(grants, grant.NewGrant(
			groupResource,
			entitlementGroupMember,
			userResource.Id,
			grant.WithGrantMetadata(map[string]any{
				profileFieldGroupName: groupResource.DisplayName,
				profileFieldUsername:  user.UserName,
			}),
		))
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}
	return grants, &rs.SyncOpResults{
		NextPageToken: outToken,
		Annotations:   annos,
	}, nil
}

// Grant adds a user to a group by calling the UpdateGroupUsers API.
func (g *groupBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	if principal.Id.ResourceType != userResourceType.Id {
		return nil, nil, fmt.Errorf("invalid principal type: expected %s, got %s", userResourceType.Id, principal.Id.ResourceType)
	}

	groupID := entitlement.Resource.Id.Resource
	userID := principal.Id.Resource

	_, annos, err := g.client.UpdateGroupUsers(ctx, groupID, buildGroupUsersRequest(userID))
	if err != nil {
		return nil, annos, fmt.Errorf("failed to grant group membership: %w", err)
	}

	return nil, annos, nil
}

// Revoke removes a user from a group.
func (g *groupBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	groupID := grantObj.Entitlement.Resource.Id.Resource
	userID := grantObj.Principal.Id.Resource

	_, annos, err := g.client.DeleteGroupUsers(ctx, groupID, buildGroupUsersRequest(userID))
	if err != nil {
		return annos, fmt.Errorf("failed to remove user %s from group %s: %w", userID, groupID, err)
	}

	return annos, nil
}

// buildGroupUsersRequest creates a request to add/remove a user from a group.
func buildGroupUsersRequest(userID string) client.GroupUsersRequest {
	return client.GroupUsersRequest{
		Users: []client.GroupUserIdentifier{
			{UserId: userID},
		},
	}
}

// newGroupBuilder constructs a groupBuilder with the provided API client.
func newGroupBuilder(client *client.Client) *groupBuilder {
	return &groupBuilder{
		resourceType: groupResourceType,
		client:       client,
	}
}

// parseIntoGroupResource maps a client.Group to a Baton v2.Resource.
func parseIntoGroupResource(group *client.Group) (*v2.Resource, error) {
	profile := map[string]any{
		profileFieldGroupName: group.GroupName,
		"group_type":          group.GroupType,
		"users_count":         group.UsersCount,
	}

	return rs.NewGroupResource(
		group.GroupName,
		groupResourceType,
		group.GroupId,
		[]rs.GroupTraitOption{
			rs.WithGroupProfile(profile),
		},
	)
}
