package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

var _ connectorbuilder.AccountManager = &userBuilder{}

// userBuilder handles user resource management and permission assignments.
type userBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

// ResourceType returns the Baton resource type handled by this builder.
func (b *userBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return userResourceType
}

// List retrieves all users from DocuSign API and converts them to Baton resources.
// Uses pagination to handle large datasets efficiently.
func (b *userBuilder) List(
	ctx context.Context,
	_ *v2.ResourceId,
	pToken *pagination.Token,
) ([]*v2.Resource, string, annotations.Annotations, error) {
	var resources []*v2.Resource

	bag, pageToken, err := parsePageToken(pToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, "", nil, err
	}

	users, nextPageToken, annotation, err := b.client.GetUsers(ctx, client.PageOptions{
		PageSize:  pToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, "", nil, err
	}

	for _, user := range users {
		userResource, err := parseIntoUserResource(&user)
		if err != nil {
			return nil, "", nil, err
		}

		resources = append(resources, userResource)
	}
	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, "", nil, err
		}
	}

	return resources, outToken, annotation, nil
}

func (b *userBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// Grants assigns permissions to users based on their DocuSign settings.
func (b *userBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant
	var annos annotations.Annotations
	userID := resource.Id

	userDetail, annotation, err := b.client.GetUserDetails(ctx, userID.Resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to fetch details for %s: %w", userID.Resource, err)
	}

	for _, annon := range annotation {
		annos.Append(annon)
	}

	permissionProfileID := userDetail.PermissionProfileID
	// A non-active user will not have any PP assigned, so this field can be empty.
	if permissionProfileID == "" {
		return nil, "", nil, nil
	}

	permissionProfileResource := &v2.Resource{
		Id: &v2.ResourceId{
			ResourceType: permissionProfilesResourceType.Id,
			Resource:     permissionProfileID,
		},
	}

	newGrant := grant.NewGrant(permissionProfileResource, permissionProfileAssignedTag, userID)
	grants = append(grants, newGrant)

	return grants, "", annos, nil
}

// CreateAccountCapabilityDetails declares support for account provisioning without a password.
func (b *userBuilder) CreateAccountCapabilityDetails(_ context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

// CreateAccount provisions a new DocuSign user based on AccountInfo and CredentialOptions.
func (b *userBuilder) CreateAccount(
	ctx context.Context,
	accountInfo *v2.AccountInfo,
	credentialOptions *v2.LocalCredentialOptions,
) (
	connectorbuilder.CreateAccountResponse,
	[]*v2.PlaintextData,
	annotations.Annotations,
	error,
) {
	pMap := accountInfo.Profile.AsMap()
	annos := annotations.Annotations{}

	email, ok := pMap["email"].(string)
	if !ok || email == "" {
		return nil, nil, nil, fmt.Errorf("email is required")
	}

	username, ok := pMap["username"].(string)
	if !ok || username == "" {
		return nil, nil, nil, fmt.Errorf("username is required")
	}

	usersRequest := client.CreateUsersRequest{
		NewUsers: []client.NewUser{{
			UserName: username,
			Email:    email,
		}},
	}

	createdUsers, annotation, err := b.client.CreateUsers(ctx, usersRequest)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(createdUsers.NewUsers) == 0 {
		return nil, nil, nil, fmt.Errorf("no user returned from API")
	}

	for _, annon := range annotation {
		annos.Append(annon)
	}

	created := createdUsers.NewUsers[0]
	if created.ErrorDetails != nil {
		return nil, nil, nil, fmt.Errorf("failed to create user: %s - %s",
			created.ErrorDetails.ErrorCode, created.ErrorDetails.Message)
	}

	userRes, err := parseIntoUserResource(&client.User{
		UserId:     created.UserId,
		UserName:   created.UserName,
		Email:      created.Email,
		UserStatus: created.UserStatus,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	return &v2.CreateAccountResponse_SuccessResult{
		Resource: userRes,
	}, nil, annos, nil
}

// Delete implements account deprovisioning for users.
func (b *userBuilder) Delete(ctx context.Context, resourceId *v2.ResourceId) (annotations.Annotations, error) {
	if resourceId.ResourceType != userResourceType.Id {
		return nil, fmt.Errorf("invalid resource type: expected %s, got %s", userResourceType.Id, resourceId.ResourceType)
	}

	userId := resourceId.Resource
	deleteRequest := client.DeleteUsersRequest{
		Users: []client.UserIdentifier{
			{UserId: userId},
		},
	}

	response, annotation, err := b.client.DeleteUsers(ctx, deleteRequest)
	if err != nil {
		return annotation, fmt.Errorf("error deleting user: %w", err)
	}

	if response == nil {
		return annotation, fmt.Errorf("unexpected nil response when deleting user %s", userId)
	}

	if len(response.Users) > 0 {
		deletedUser := response.Users[0]
		if deletedUser.ErrorDetails != nil {
			return annotation, fmt.Errorf("failed to delete user %s: %s - %s",
				userId, deletedUser.ErrorDetails.ErrorCode, deletedUser.ErrorDetails.Message)
		}
	}

	return annotation, nil
}

// newUserBuilder constructs a userBuilder with the provided API client.
func newUserBuilder(client *client.Client) *userBuilder {
	return &userBuilder{
		resourceType: userResourceType,
		client:       client,
	}
}

// parseIntoUserResource maps a client.User object into a Baton v2.Resource.
func parseIntoUserResource(user *client.User) (*v2.Resource, error) {
	var userStatus v2.UserTrait_Status_Status
	switch user.UserStatus {
	case "Active":
		userStatus = v2.UserTrait_Status_STATUS_ENABLED
	case "Disabled", "ActivationRequired", "ActivationSent":
		userStatus = v2.UserTrait_Status_STATUS_DISABLED
	case "Closed": // When you delete a user, their status changes to closed. We will treat this as deleted.
		userStatus = v2.UserTrait_Status_STATUS_DELETED
	default:
		userStatus = v2.UserTrait_Status_STATUS_UNSPECIFIED
	}

	profile := map[string]any{
		"userName":   user.UserName,
		"email":      user.Email,
		"isAdmin":    user.IsAdmin,
		"permission": user.Permission,
		"status":     user.UserStatus,
	}

	userTraits := []resource.UserTraitOption{
		resource.WithStatus(userStatus),
		resource.WithUserProfile(profile),
		resource.WithUserLogin(user.UserName),
		resource.WithEmail(user.Email, true),
	}

	return resource.NewUserResource(
		user.UserName,
		userResourceType,
		user.UserId,
		userTraits,
	)
}
