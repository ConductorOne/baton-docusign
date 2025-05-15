package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

// UserClient defines the interface for DocuSign user API operations.
type UserClient interface {
	GetUsers(ctx context.Context, options client.PageOptions) ([]client.User, string, annotations.Annotations, error)
	GetUserDetails(ctx context.Context, userID string) (*client.UserDetail, annotations.Annotations, error)
	CreateUsers(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error)
}

// userBuilder handles user resource management and permission assignments.
type userBuilder struct {
	resourceType      *v2.ResourceType
	client            UserClient
	permissionBuilder *permissionBuilder
}

// ResourceType returns the Baton resource type handled by this builder.
func (b *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// List retrieves all users from DocuSign API and converts them to Baton resources.
// Uses pagination to handle large datasets efficiently.
func (b *userBuilder) List(
	ctx context.Context,
	parentResourceID *v2.ResourceId,
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
		userCopy := user
		userResource, err := parseIntoUserResource(&userCopy)
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

// Entitlements returns empty as users don't have direct entitlements in this implementation.
// Entitlements are managed separately by the permissionBuilder.
func (b *userBuilder) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// Grants assigns permissions to users based on their DocuSign settings.
// Uses permissionBuilder to ensure all grants reference the central permission resource.
func (b *userBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	userId := resource.Id.Resource

	permissionResource, err := b.permissionBuilder.GetPermissionResource(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get permission resource: %w", err)
	}

	var grants []*v2.Grant

	detail, annotation, err := b.client.GetUserDetails(ctx, userId)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to fetch details for %s: %w", userId, err)
	}

	for _, annon := range annotation {
		annos.Append(annon)
	}

	userGrants, err := createUserGrants(permissionResource, detail)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to create grants for %s: %w", userId, err)
	}
	grants = append(grants, userGrants...)

	return grants, "", annos, nil
}

// CreateAccountCapabilityDetails declares support for account provisioning without a password.
func (b *userBuilder) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
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
	credentialOptions *v2.CredentialOptions,
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

// newUserBuilder constructs a userBuilder with the provided API client.
func newUserBuilder(client *client.Client, pb *permissionBuilder) *userBuilder {
	return &userBuilder{
		resourceType:      userResourceType,
		client:            client,
		permissionBuilder: pb,
	}
}

// parseIntoUserResource maps a client.User object into a Baton v2.Resource.
func parseIntoUserResource(user *client.User) (*v2.Resource, error) {
	var userStatus v2.UserTrait_Status_Status
	switch user.UserStatus {
	case "Active":
		userStatus = v2.UserTrait_Status_STATUS_ENABLED
	case "Disabled", "Closed", "ActivationRequired", "ActivationSent":
		userStatus = v2.UserTrait_Status_STATUS_DISABLED
	default:
		userStatus = v2.UserTrait_Status_STATUS_UNSPECIFIED
	}

	profile := map[string]interface{}{
		"userName":   user.UserName,
		"email":      user.Email,
		"isAdmin":    user.IsAdmin,
		"permission": user.Permission,
		"status":     user.UserStatus,
	}

	userTraits := []resource.UserTraitOption{
		resource.WithUserProfile(profile),
		resource.WithStatus(userStatus),
		resource.WithUserLogin(user.UserName),
	}

	return resource.NewUserResource(
		user.UserName,
		userResourceType,
		user.UserId,
		userTraits,
	)
}
