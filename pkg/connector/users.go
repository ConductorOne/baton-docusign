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

// UserClient defines the minimal interface for user-related API operations.
type UserClient interface {
	GetUsers(ctx context.Context, token string) ([]client.User, string, annotations.Annotations, error)
	CreateUsers(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error)
}

// userBuilder implements resource listing and provisioning for DocuSign users.
type userBuilder struct {
	resourceType *v2.ResourceType
	client       UserClient
}

// ResourceType returns the Baton resource type handled by this builder.
func (b *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// List fetches users from the API, converts them to Baton resources, and returns pagination info.
func (b *userBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var resources []*v2.Resource
	annos := annotations.Annotations{}

	var token string
	if pToken != nil {
		token = pToken.Token
	}

	users, nextToken, newAnnos, err := b.client.GetUsers(ctx, token)
	if err != nil {
		return nil, "", nil, err
	}

	for _, a := range newAnnos {
		annos.Append(a)
	}

	for _, user := range users {
		userCopy := user
		userResource, err := parseIntoUserResource(&userCopy)
		if err != nil {
			return nil, "", nil, err
		}
		resources = append(resources, userResource)
	}

	return resources, nextToken, annos, nil
}

// Entitlements returns no entitlements for users (not supported).
func (b *userBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// Grants returns no grants for users (not supported).
func (b *userBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
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

	createdUsers, _, err := b.client.CreateUsers(ctx, usersRequest)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(createdUsers.NewUsers) == 0 {
		return nil, nil, nil, fmt.Errorf("no user returned from API")
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
	}, nil, nil, nil
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
