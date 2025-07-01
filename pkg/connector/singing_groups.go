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

type signingGroupsClientInterface interface {
	GetSigningGroups(ctx context.Context, options client.PageOptions) ([]client.SigningGroup, string, annotations.Annotations, error)
	GetSigningGroupUsers(ctx context.Context, groupID string, options client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	GetUserByEmail(ctx context.Context, userEmail string) (*client.User, annotations.Annotations, error)
}

type signingGroupBuilder struct {
	resourceType *v2.ResourceType
	client       signingGroupsClientInterface
}

func (g *signingGroupBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return signingGroupResourceType
}

func (g *signingGroupBuilder) List(ctx context.Context, _ *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var (
		sGroups []*v2.Resource
		anno    annotations.Annotations
	)

	bag, pageToken, err := parsePageToken(pToken.Token, &v2.ResourceId{ResourceType: signingGroupResourceType.Id})
	if err != nil {
		return nil, "", nil, err
	}
	signingGroups, nextPageToken, newAnnos, err := g.client.GetSigningGroups(ctx, client.PageOptions{
		PageSize:  pToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, "", nil, err
	}

	for _, newAnnotation := range newAnnos {
		anno.Append(newAnnotation)
	}

	for _, signingGroup := range signingGroups {
		signingGroupResource, err := parseIntoSigningGroupResource(&signingGroup)
		if err != nil {
			return nil, "", nil, err
		}
		sGroups = append(sGroups, signingGroupResource)
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, "", nil, err
		}
	}

	return sGroups, outToken, anno, nil
}

func (g *signingGroupBuilder) Entitlements(_ context.Context, groupResource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	newEntitlement := entitlement.NewAssignmentEntitlement(
		groupResource,
		entitlementGroupMember,
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(fmt.Sprintf("Member of %s", groupResource.DisplayName)),
		entitlement.WithDescription(fmt.Sprintf("Member of %s signing group", groupResource.DisplayName)),
	)
	return []*v2.Entitlement{newEntitlement}, "", nil, nil
}

func (g *signingGroupBuilder) Grants(ctx context.Context, groupResource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	bag, pageToken, err := parsePageToken(pToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, "", nil, err
	}
	signingGroupMembers, nextPageToken, annos, err := g.client.GetSigningGroupUsers(ctx, groupResource.Id.Resource, client.PageOptions{
		PageSize:  pToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("docusign-connector: failed to get group users for %s: %w", groupResource.Id.Resource, err)
	}
	grants := make([]*v2.Grant, 0, len(signingGroupMembers))
	for _, user := range signingGroupMembers {
		userDetails, _, err := g.client.GetUserByEmail(ctx, user.Email)
		if err != nil {
			return nil, "", nil, err
		}

		userResource := &v2.Resource{
			Id: &v2.ResourceId{
				ResourceType: userResourceType.Id,
				Resource:     userDetails.UserId,
			},
		}
		grants = append(grants, grant.NewGrant(
			groupResource,
			entitlementGroupMember,
			userResource.Id,
			grant.WithGrantMetadata(map[string]interface{}{
				"signing_group_id":   groupResource.Id.Resource,
				"signing_group_name": groupResource.DisplayName,
				"user_id":            userDetails.UserId,
				"email":              userDetails.Email,
				"username":           userDetails.UserName,
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

// newSigningGroupBuilder constructs a signingGroupBuilder with the provided API client.
func newSigningGroupBuilder(client *client.Client) *signingGroupBuilder {
	return &signingGroupBuilder{
		resourceType: groupResourceType,
		client:       client,
	}
}

// parseIntoSigningGroupResource maps a client.SigningGroup to a Baton v2.Resource.
func parseIntoSigningGroupResource(group *client.SigningGroup) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_name": group.GroupName,
		"group_type": group.GroupType,
		"created":    group.Created,
	}

	return resource.NewGroupResource(
		group.GroupName,
		signingGroupResourceType,
		group.SigningGroupId,
		[]resource.GroupTraitOption{
			resource.WithGroupProfile(profile),
		},
	)
}
