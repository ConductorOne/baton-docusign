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

type UserClient interface {
	GetUsers(ctx context.Context) ([]client.User, annotations.Annotations, error)
	GetUserGroups(ctx context.Context, userID string) ([]client.Group, annotations.Annotations, error)
	CreateUsers(ctx context.Context, request client.CreateUsersRequest) (*client.UserCreationResponse, annotations.Annotations, error)
}

type userBuilder struct {
	resourceType *v2.ResourceType
	client       UserClient
}

// ResourceType returns the resource type handled by this builder.
func (b *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// List fetches all users from the DocuSign API and converts them to Baton resources.
func (b *userBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var resources []*v2.Resource
	annos := annotations.Annotations{}

	users, newAnnos, err := b.client.GetUsers(ctx)
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

	return resources, "", annos, nil
}

// Entitlements returns the list of entitlements for a user (not implemented here).
func (b *userBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// Grants returns the groups a user is a member of.
func (b *userBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant
	annos := annotations.Annotations{}

	userGroups, newAnnos, err := b.client.GetUserGroups(ctx, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("error fetching user groups: %w", err)
	}

	for _, a := range newAnnos {
		annos.Append(a)
	}

	for _, group := range userGroups {
		groupResource, err := parseIntoGroupResource(&group)
		if err != nil {
			return nil, "", nil, err
		}

		grants = append(grants, grant.NewGrant(
			groupResource,
			"member",
			resource.Id,
		))
	}

	return grants, "", annos, nil
}

// CreateAccountCapabilityDetails declares support for account provisioning without passwords.
func (b *userBuilder) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

// CreateAccount creates a new user in DocuSign using the given profile information.
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

// newUserBuilder constructs a new userBuilder with the given client.
func newUserBuilder(client *client.Client) *userBuilder {
	return &userBuilder{
		resourceType: userResourceType,
		client:       client,
	}
}

// parseIntoUserResource maps a DocuSign user into a Baton v2.Resource.
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
