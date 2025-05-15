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

// Entitlement value representing group membership.
const (
	entitlementGroupMember = "member"
)

// groupsClientInterface defines the methods required for group-related API calls.
type groupsClientInterface interface {
	GetGroups(ctx context.Context, options client.PageOptions) ([]client.Group, string, annotations.Annotations, error)
	GetGroupUsers(ctx context.Context, groupID string, options client.PageOptions) ([]client.User, string, annotations.Annotations, error)
}

// groupBuilder implements resource listing, entitlements, and grants for DocuSign groups.
type groupBuilder struct {
	resourceType *v2.ResourceType
	client       groupsClientInterface
}

// ResourceType returns the Baton resource type handled by this builder.
func (g *groupBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return groupResourceType
}

// List fetches groups from the API, converts them to Baton resources, and returns pagination info.
func (g *groupBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var resources []*v2.Resource
	annos := annotations.Annotations{}
	bag, pageToken, err := parsePageToken(pToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, "", nil, err
	}
	groups, nextPageToken, newAnnos, err := g.client.GetGroups(ctx, client.PageOptions{
		PageSize:  pToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, "", nil, err
	}

	for _, annon := range newAnnos {
		annos.Append(annon)
	}

	for _, group := range groups {
		groupCopy := group
		groupResource, err := parseIntoGroupResource(&groupCopy)
		if err != nil {
			return nil, "", nil, err
		}
		resources = append(resources, groupResource)
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, "", nil, err
		}
	}

	return resources, outToken, annos, nil
}

// Entitlements returns a "member" entitlement for each group, grantable to users.
func (g *groupBuilder) Entitlements(ctx context.Context, groupResource *v2.Resource, pToken *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	ent := entitlement.NewAssignmentEntitlement(
		groupResource,
		entitlementGroupMember,
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(fmt.Sprintf("Member of %s", groupResource.DisplayName)),
		entitlement.WithDescription(fmt.Sprintf("Member of %s group", groupResource.DisplayName)),
	)
	return []*v2.Entitlement{ent}, "", annos, nil
}

// Grants fetches users in the group and returns grants for the "member" entitlement.
func (g *groupBuilder) Grants(ctx context.Context, groupResource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	bag, pageToken, err := parsePageToken(pToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, "", nil, err
	}
	groupUsers, nextPageToken, annos, err := g.client.GetGroupUsers(ctx, groupResource.Id.Resource, client.PageOptions{
		PageSize:  pToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("docusign-connector: failed to get group users for %s: %w", groupResource.Id.Resource, err)
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
			grant.WithGrantMetadata(map[string]interface{}{
				"group_id":   groupResource.Id.Resource,
				"group_name": groupResource.DisplayName,
				"user_id":    user.UserId,
				"username":   user.UserName,
			}),
		))
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, "", nil, err
		}
	}
	return grants, outToken, annos, nil
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
