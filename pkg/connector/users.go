package connector

import (
	"context"
	"fmt"
	"sync"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var _ connectorbuilder.AccountManagerV2 = &userBuilder{}

// userBuilder handles user resource management and permission assignments.
type userBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client

	// skipPermissionProfileResourceType reports whether permission_profile is
	// excluded from the sync filter. It selects the annotation: normally only
	// entitlements are skipped, but when permission_profile is excluded the
	// grants pass is skipped too, since it is this builder's only output.
	skipPermissionProfileResourceType bool

	// permissionProfilesMu/permissionProfilesCached/permissionProfiles/permissionProfilesErr
	// memoize the one account-wide GetPermissionProfiles call tryFastPathGrant's
	// name-lookup branch needs, across every Active user's Grants() call in this sync (a
	// *userBuilder is constructed once per sync and Grants() runs concurrently across
	// users, so this must be shared and safe for concurrent access — hence a mutex, not
	// a plain bool). uhttp's GET cache only ever caches a 200 response, never an error,
	// so without this a persistent failure (e.g. a service user lacking
	// permission_profiles read access) would re-hit the real API on every Active user
	// instead of once per sync — doubling that user's calls (the failed lookup, then the
	// GetUserDetails fallback) against the same hourly budget this fix exists to
	// protect. Only a genuinely persistent failure is cached this way, though — see
	// isCacheablePermissionProfilesError's doc for why a transient failure (a rate limit,
	// a plain 5xx/network blip, a context error) is deliberately left uncached.
	permissionProfilesMu     sync.Mutex
	permissionProfilesCached bool
	permissionProfiles       []client.PermissionProfile
	permissionProfilesErr    error
}

// isCacheablePermissionProfilesError reports whether err is a persistent,
// account-configuration-shaped failure safe to cache on userBuilder for the rest of this
// sync — mirrors isOptInFeatureUnavailableError's PermissionDenied/Unauthenticated/
// NotFound classification (this account's permission_profiles access isn't going to
// change mid-sync), deliberately narrower than that helper: no FailedPrecondition, which
// is specific to CLM discovery's response-shape check and not relevant here.
//
// Everything else is deliberately NOT cacheable — a reclassified rate-limit error
// (codes.Unavailable with a RateLimitDescription), an ordinary transient 5xx/network
// blip (codes.Unavailable with none), a context cancellation/deadline, or any other
// unclassified failure. Caching any of these would be actively harmful, not just a
// missed optimization: the SDK's per-action retry loop (pkg/sync/parallel_syncer.go,
// unlimited attempts) reuses this same userBuilder across every retry of this action, so
// a cached transient error would replay the same stale failure on every retry forever,
// without ever issuing a fresh request to notice the underlying condition — whether an
// hourly rate-limit window resetting or a 503 clearing — has cleared. A context error
// specifically only means whichever caller's context happened to win this call was
// already done, not that the account or its permissions are actually broken; caching it
// would incorrectly drop every other Active user in the sync back to the per-user
// GetUserDetails fallback for the rest of the run.
func isCacheablePermissionProfilesError(err error) bool {
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound:
		return true
	default:
		return false
	}
}

// getPermissionProfiles returns the account's permission profiles, calling
// client.GetPermissionProfiles at most once for the lifetime of this userBuilder unless
// the call fails with a non-cacheable (transient) error — see the memoization fields'
// doc on the struct above, and isCacheablePermissionProfilesError's doc, for why.
func (b *userBuilder) getPermissionProfiles(ctx context.Context) ([]client.PermissionProfile, error) {
	b.permissionProfilesMu.Lock()
	defer b.permissionProfilesMu.Unlock()

	if b.permissionProfilesCached {
		return b.permissionProfiles, b.permissionProfilesErr
	}

	profiles, _, err := b.client.GetPermissionProfiles(ctx)
	if err != nil && !isCacheablePermissionProfilesError(err) {
		return nil, err
	}

	b.permissionProfilesCached = true
	b.permissionProfiles = profiles
	b.permissionProfilesErr = err
	return profiles, err
}

// ResourceType returns the Baton resource type handled by this builder,
// annotated to tell the SDK's sync engine whether it can skip calling
// Entitlements()/Grants() for user resources. userResourceType is a
// package-level var shared with other code, so it's cloned before its
// annotations are mutated.
func (b *userBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	rt := proto.Clone(userResourceType).(*v2.ResourceType)
	annos := annotations.Annotations(rt.Annotations)
	if b.skipPermissionProfileResourceType {
		annos.Update(&v2.SkipEntitlementsAndGrants{})
	} else {
		annos.Update(&v2.SkipEntitlements{})
	}
	rt.Annotations = annos
	return rt
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
// This method exists solely to emit the cross-type permission_profile grant. When the
// customer's sync filter excludes permission_profile, the SDK's sync engine skips
// calling Grants() entirely for user resources based on the SkipEntitlementsAndGrants
// annotation ResourceType() attaches in that case, so this method itself no longer needs
// to guard against that case.
//
// Tries tryFastPathGrant first (an Active user's permission-profile ID or NAME, already
// captured on the resource's profile during List(), resolved without a per-user
// GetUserDetails call — avoiding the N+1 pattern that contributes to DocuSign's hourly
// rate limit) and falls back to the always-correct per-user GetUserDetails path,
// unchanged from before that fast path existed, whenever it declines to handle the
// request (see tryFastPathGrant's own doc).
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

// tryFastPathGrant is Grants' fast path for an Active user, avoiding the per-user
// GetUserDetails call that contributes to DocuSign's hourly rate limit. Two ways it can
// resolve the grant without that call, both already captured on the resource's profile
// during List():
//   - Preferred: client.User.PermissionProfileID directly, if the list response included
//     it (unconfirmed against a live account — see that field's doc) — no API call at all.
//   - Otherwise: the permission-profile NAME, resolved to an ID via GetPermissionProfiles
//     — one account-wide call for the whole sync, via getPermissionProfiles' own
//     memoization on this builder (see its doc), not uhttp's GET cache: that cache never
//     stores a non-2xx response, so relying on it alone would let a persistent failure
//     (not just a rate limit — e.g. a service user lacking permission_profiles read
//     access) re-hit the real API once per Active user instead of once per sync, the
//     same 1:1 amplification as the GetUserDetails path this fast path exists to avoid.
//
// handled=false means "no decision, fall back to Grants' original GetUserDetails path
// unchanged" — covers a non-active user, a missing profile field, an unresolvable name
// (profile renamed/deleted since listing), or a GetPermissionProfiles failure unrelated
// to rate limiting. handled=true with a non-nil err means GetPermissionProfiles hit the
// same rate limit GetUserDetails would also hit — identified specifically via
// isReclassifiedRateLimitError, not codes.Unavailable alone (that code is broader —
// uhttp also maps a plain 503 or a transient network failure to it, and those should
// still fall back rather than fail every active user's Grants for the rest of the sync).
// Propagating the rate-limit case directly, rather than also falling back, avoids every
// active user paying for two failing calls instead of one while the account is already
// over budget — exactly the amplification this fix exists to reduce. handled=true with a
// nil err means the grant was resolved.
//
// Never forwards GetPermissionProfiles' annotations: after the first real call in a
// sync, repeat calls are served from uhttp's GET cache, which replays that first
// response's rate-limit snapshot verbatim — forwarding it on every subsequent active
// user would feed the SDK's self-throttling rate limiter a frozen, increasingly stale
// signal instead of the fresh per-request data GetUserDetails supplied before this fast
// path existed.
func (b *userBuilder) tryFastPathGrant(ctx context.Context, resource *v2.Resource, userID *v2.ResourceId) (*v2.Grant, annotations.Annotations, error, bool) {
	profile := rs.GetProfile(resource)

	userStatus, ok := rs.GetProfileStringValue(profile, profileFieldStatus)
	if !ok || userStatus != userStatusActive {
		return nil, nil, nil, false
	}

	// If the list response happened to include the profile ID directly (unconfirmed
	// against a live account — see client.User.PermissionProfileID's doc), this skips
	// GetPermissionProfiles entirely: no API call, no name lookup, no cache dependency.
	if id, ok := rs.GetProfileStringValue(profile, profileFieldPermissionID); ok && id != "" {
		newGrant := grant.NewGrant(
			&v2.Resource{Id: &v2.ResourceId{ResourceType: permissionProfilesResourceType.Id, Resource: id}},
			permissionProfileAssignedTag,
			userID,
		)
		return newGrant, nil, nil, true
	}

	name, ok := rs.GetProfileStringValue(profile, profileFieldPermission)
	if !ok || name == "" {
		return nil, nil, nil, false
	}

	profiles, err := b.getPermissionProfiles(ctx)
	if err != nil {
		if isReclassifiedRateLimitError(err) {
			return nil, nil, err, true
		}
		ctxzap.Extract(ctx).Debug("baton-docusign: GetPermissionProfiles failed, falling back to per-user GetUserDetails for this Grants call",
			zap.String("user_id", userID.Resource), zap.Error(err))
		return nil, nil, nil, false
	}

	id, matches := permissionProfileIDByName(profiles, name)
	if matches != 1 {
		return nil, nil, nil, false
	}
	newGrant := grant.NewGrant(
		&v2.Resource{Id: &v2.ResourceId{ResourceType: permissionProfilesResourceType.Id, Resource: id}},
		permissionProfileAssignedTag,
		userID,
	)
	return newGrant, nil, nil, true
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
// skipPermissionProfileResourceType controls the resource-type annotation ResourceType()
// attaches (SkipEntitlements vs. SkipEntitlementsAndGrants); pass false when
// the customer's sync filter excludes the permission_profile resource type.
func newUserBuilder(client *client.Client, skipPermissionProfileResourceType bool) *userBuilder {
	return &userBuilder{
		resourceType:                      userResourceType,
		client:                            client,
		skipPermissionProfileResourceType: skipPermissionProfileResourceType,
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
		"userName":               user.UserName,
		profileFieldEmail:        user.Email,
		"isAdmin":                user.IsAdmin,
		profileFieldPermission:   user.Permission,
		profileFieldStatus:       user.UserStatus,
		profileFieldPermissionID: user.PermissionProfileID,
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
