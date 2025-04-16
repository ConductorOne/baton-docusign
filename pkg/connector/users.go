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

type userBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (o *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

func (o *userBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var resources []*v2.Resource
	annos := annotations.Annotations{}

	users, newAnnos, err := o.client.GetUsers(ctx)
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

func (o *userBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (o *userBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant
	annos := annotations.Annotations{}

	userGroups, newAnnos, err := o.client.GetUserGroups(ctx, resource.Id.Resource)
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

		grant := grant.NewGrant(
			groupResource,
			"member",
			resource.Id,
		)
		grants = append(grants, grant)
	}

	return grants, "", annos, nil
}

func (b *userBuilder) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

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
	// Extract profile fields
	pMap := accountInfo.Profile.AsMap()

	email, ok := pMap["email"].(string)
	if !ok || email == "" {
		return nil, nil, nil, fmt.Errorf("email is required")
	}

	username, ok := pMap["username"].(string)
	if !ok || username == "" {
		return nil, nil, nil, fmt.Errorf("username is required")
	}

	newUser := client.NewUser{
		UserName: username,
		Email:    email,
	}

	usersRequest := client.CreateUsersRequest{
		NewUsers: []client.NewUser{newUser},
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

	car := &v2.CreateAccountResponse_SuccessResult{
		Resource: userRes,
	}

	return car, nil, nil, nil
}

func newUserBuilder(client *client.Client) *userBuilder {
	return &userBuilder{
		resourceType: userResourceType,
		client:       client,
	}
}

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
