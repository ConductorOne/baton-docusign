package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ connectorbuilder.AccountManagerV2 = &userBuilder{}

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
	attr rs.SyncOpAttrs,
) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var resources []*v2.Resource

	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: userResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	users, nextPageToken, annotation, err := b.client.GetUsers(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, nil, err
	}

	for _, user := range users {
		userResource, err := parseIntoUserResource(&user)
		if err != nil {
			return nil, nil, err
		}

		resources = append(resources, userResource)
	}
	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}

	return resources, &rs.SyncOpResults{
		Annotations:   annotation,
		NextPageToken: outToken,
	}, nil
}

func (b *userBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants assigns permissions to users based on their DocuSign settings.
//
// Tries tryFastPathGrant first (an Active user's permission-profile NAME, already
// captured on the resource's profile during List(), resolved via one account-wide
// GetPermissionProfiles call instead of a per-user GetUserDetails call — the N+1
// pattern Pylon #11445 flagged as contributing to DocuSign's hourly rate limit) and
// falls back to the always-correct per-user GetUserDetails path unchanged from before
// that fast path existed whenever it declines to handle the request (see its own doc).
func (b *userBuilder) Grants(ctx context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	userID := resource.Id

	if newGrant, annos, err, handled := b.tryFastPathGrant(ctx, resource, userID); handled {
		if err != nil {
			return nil, nil, err
		}
		return []*v2.Grant{newGrant}, &rs.SyncOpResults{Annotations: annos}, nil
	}

	userDetail, annotation, err := b.client.GetUserDetails(ctx, userID.Resource)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch details for %s: %w", userID.Resource, err)
	}

	var annos annotations.Annotations
	for _, annon := range annotation {
		annos.Append(annon)
	}

	permissionProfileID := userDetail.PermissionProfileID
	// A non-active user will not have any PP assigned, so this field can be empty.
	if permissionProfileID == "" {
		return nil, nil, nil
	}

	permissionProfileResource := &v2.Resource{
		Id: &v2.ResourceId{
			ResourceType: permissionProfilesResourceType.Id,
			Resource:     permissionProfileID,
		},
	}

	return []*v2.Grant{
		grant.NewGrant(permissionProfileResource, permissionProfileAssignedTag, userID),
	}, &rs.SyncOpResults{Annotations: annos}, nil
}

// tryFastPathGrant is Grants' fast path: an Active user's permission-profile NAME,
// already captured on the resource's profile during List(), resolved via
// GetPermissionProfiles (one account-wide call, already served from uhttp's default GET
// cache on every call after the first in this sync — see pkg/client/clm_client.go's
// WithNoCache usage for this repo's opt-out convention when a fresh read matters, which
// this doesn't need) instead of a per-user GetUserDetails call.
//
// handled=false means "no decision, fall back to Grants' original GetUserDetails path
// unchanged" — covers a non-active user, a missing profile field, an unresolvable name
// (profile renamed/deleted since listing), or a GetPermissionProfiles failure unrelated
// to rate limiting. handled=true with a non-nil err means GetPermissionProfiles hit the
// same rate limit GetUserDetails would also hit (codes.Unavailable — see
// pkg/client/helper.go); propagate it directly rather than also burning that call —
// otherwise every active user would pay for two failing calls instead of one while the
// account is already over budget, exactly the amplification this fix exists to reduce.
// handled=true with a nil err means the grant was resolved.
func (b *userBuilder) tryFastPathGrant(ctx context.Context, resource *v2.Resource, userID *v2.ResourceId) (*v2.Grant, annotations.Annotations, error, bool) {
	profile := rs.GetProfile(resource)

	userStatus, ok := rs.GetProfileStringValue(profile, profileFieldStatus)
	if !ok || userStatus != userStatusActive {
		return nil, nil, nil, false
	}
	name, ok := rs.GetProfileStringValue(profile, profileFieldPermission)
	if !ok || name == "" {
		return nil, nil, nil, false
	}

	profiles, annos, err := b.client.GetPermissionProfiles(ctx)
	if err != nil {
		if grpcstatus.Code(err) == codes.Unavailable {
			return nil, nil, err, true
		}
		return nil, nil, nil, false
	}

	id, ok := permissionProfileIDByName(profiles, name)
	if !ok {
		return nil, nil, nil, false
	}
	newGrant := grant.NewGrant(
		&v2.Resource{Id: &v2.ResourceId{ResourceType: permissionProfilesResourceType.Id, Resource: id}},
		permissionProfileAssignedTag,
		userID,
	)
	return newGrant, annos, nil, true
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

	email, ok := pMap[profileFieldEmail].(string)
	if !ok || email == "" {
		return nil, nil, nil, fmt.Errorf("email is required")
	}

	username, ok := pMap[profileFieldUsername].(string)
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
	case userStatusActive:
		userStatus = v2.UserTrait_Status_STATUS_ENABLED
	case "Disabled", "ActivationRequired", "ActivationSent":
		userStatus = v2.UserTrait_Status_STATUS_DISABLED
	case "Closed": // When you delete a user, their status changes to closed. We will treat this as deleted.
		userStatus = v2.UserTrait_Status_STATUS_DELETED
	default:
		userStatus = v2.UserTrait_Status_STATUS_UNSPECIFIED
	}

	profile := map[string]any{
		"userName":             user.UserName,
		profileFieldEmail:      user.Email,
		"isAdmin":              user.IsAdmin,
		profileFieldPermission: user.Permission,
		profileFieldStatus:     user.UserStatus,
	}

	userTraits := []rs.UserTraitOption{
		rs.WithUserLogin(user.UserName),
		rs.WithEmail(user.Email, true),
	}

	return rs.NewUserResource(
		user.UserName,
		userResourceType,
		user.UserId,
		userTraits,
		rs.WithResourceProfile(profile),
		rs.WithResourceStatus(v2.Status_ResourceStatus(userStatus), ""),
	)
}
