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
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

type signingGroupBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (g *signingGroupBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return signingGroupResourceType
}

func (g *signingGroupBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var (
		sGroups []*v2.Resource
		anno    annotations.Annotations
	)

	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: signingGroupResourceType.Id})
	if err != nil {
		return nil, nil, err
	}
	signingGroups, nextPageToken, newAnnos, err := g.client.GetSigningGroups(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		if attr.PageToken.Token == "" && isOptInFeatureUnavailableError(err) {
			ctxzap.Extract(ctx).Info("baton-docusign: signing groups are not available for this account, skipping signing_group sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, err
	}

	for _, newAnnotation := range newAnnos {
		anno.Append(newAnnotation)
	}

	for _, signingGroup := range signingGroups {
		signingGroupResource, err := parseIntoSigningGroupResource(&signingGroup)
		if err != nil {
			return nil, nil, err
		}
		sGroups = append(sGroups, signingGroupResource)
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}

	return sGroups, &rs.SyncOpResults{
		NextPageToken: outToken,
		Annotations:   anno,
	}, nil
}

func (g *signingGroupBuilder) Entitlements(_ context.Context, groupResource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	newEntitlement := entitlement.NewAssignmentEntitlement(
		groupResource,
		entitlementGroupMember,
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDisplayName(fmt.Sprintf("Member of %s", groupResource.DisplayName)),
		entitlement.WithDescription(fmt.Sprintf("Member of %s signing group", groupResource.DisplayName)),
	)
	return []*v2.Entitlement{newEntitlement}, nil, nil
}

func (g *signingGroupBuilder) Grants(ctx context.Context, groupResource *v2.Resource, attr rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, nil, err
	}
	signingGroupMembers, nextPageToken, annos, err := g.client.GetSigningGroupUsers(ctx, groupResource.Id.Resource, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get group users for %s: %w", groupResource.Id.Resource, err)
	}
	grants := make([]*v2.Grant, 0, len(signingGroupMembers))
	for _, user := range signingGroupMembers {
		// DocuSign API does not return userId for signing group members.
		// We must lookup the user by email to get the userId.
		userDetails, _, err := g.client.GetUserByEmail(ctx, user.Email)
		if err != nil {
			l := ctxzap.Extract(ctx)
			l.Warn("docusign-connector: failed to lookup user by email for signing group member, skipping",
				zap.String("signing_group_id", groupResource.Id.Resource),
				zap.String("email", user.Email),
				zap.String("username", user.UserName),
				zap.Error(err))
			continue
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
			grant.WithGrantMetadata(map[string]any{
				"signing_group_name": groupResource.DisplayName,
				profileFieldEmail:    user.Email,
				profileFieldUsername: user.UserName,
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

// Grant adds a user to a signing group by calling the UpdateSigningGroup API.
func (g *signingGroupBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	if principal.Id.ResourceType != userResourceType.Id {
		return nil, nil, fmt.Errorf("invalid principal type: expected %s, got %s", userResourceType.Id, principal.Id.ResourceType)
	}

	signingGroupID := entitlement.Resource.Id.Resource
	userID := principal.Id.Resource

	// Get user details to obtain email and username.
	userDetails, _, err := g.client.GetUserDetails(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user details: %w", err)
	}

	_, annos, err := g.client.UpdateSigningGroup(ctx, signingGroupID, buildSigningGroupRequest(userDetails))
	if err != nil {
		return nil, annos, fmt.Errorf("failed to grant signing group membership: %w", err)
	}

	return nil, annos, nil
}

// Revoke removes a user from a signing group.
func (g *signingGroupBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	signingGroupID := grantObj.Entitlement.Resource.Id.Resource
	userID := grantObj.Principal.Id.Resource

	// Get user details to obtain email for the signing group remove request.
	userDetails, _, err := g.client.GetUserDetails(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user details for %s: %w", userID, err)
	}

	// Note: DocuSign API returns {} with 200 if user is not in the signing group (idempotent).
	// We skip membership validation to avoid costly pagination through potentially thousands of users.
	_, annos, err := g.client.DeleteSigningGroupUsers(ctx, signingGroupID, buildSigningGroupRequest(userDetails))
	if err != nil {
		return annos, fmt.Errorf("failed to remove user %s from signing group %s: %w", userID, signingGroupID, err)
	}

	return annos, nil
}

// buildSigningGroupRequest creates a request to add/remove a user from a signing group.
func buildSigningGroupRequest(userDetails *client.UserDetail) client.SigningGroupUsersRequest {
	return client.SigningGroupUsersRequest{
		Users: []client.SigningGroupUserIdentifier{
			{
				UserName: userDetails.UserName,
				Email:    userDetails.Email,
			},
		},
	}
}

// newSigningGroupBuilder constructs a signingGroupBuilder with the provided API client.
func newSigningGroupBuilder(client *client.Client) *signingGroupBuilder {
	return &signingGroupBuilder{
		resourceType: signingGroupResourceType,
		client:       client,
	}
}

// parseIntoSigningGroupResource maps a client.SigningGroup to a Baton v2.Resource.
func parseIntoSigningGroupResource(group *client.SigningGroup) (*v2.Resource, error) {
	profile := map[string]any{
		profileFieldGroupName: group.GroupName,
		"group_type":          group.GroupType,
		"created":             group.Created,
	}

	return rs.NewGroupResource(
		group.GroupName,
		signingGroupResourceType,
		group.SigningGroupId,
		[]rs.GroupTraitOption{},
		rs.WithResourceProfile(profile),
	)
}
