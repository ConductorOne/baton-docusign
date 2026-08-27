package connector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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

	// permissionProfilesMu/permissionProfilesSyncID/permissionProfilesCached/
	// permissionProfiles/permissionProfilesErr/permissionProfilesTransientFails memoize
	// the one account-wide GetPermissionProfiles
	// call tryFastPathGrant's name-lookup branch needs, across every Active user's
	// Grants() call within a single sync. This builder is registered once via
	// ResourceSyncers and reused for the lifetime of the connector process (baton-sdk's
	// connectorbuilder.NewConnector stores the returned syncers once; see
	// vendor/.../pkg/connectorbuilder/connectorbuilder.go) — in service/hosted mode
	// that's many syncs, not one — so the cache is keyed on permissionProfilesSyncID
	// (from SyncOpAttrs.SyncID, threaded through from Grants()) rather than trusted for
	// the process's whole lifetime: a mismatch means a new sync has started and the memo
	// is stale, resetting the cached result, the transient-failure counter, and the
	// rate-limit TTL fields below. Grants() runs concurrently across users within one
	// sync, so this must stay safe for concurrent access — hence a mutex, not a plain
	// bool. uhttp's GET cache only ever caches a 200 response, never an error, so without
	// this a persistent failure (e.g. a service user lacking permission_profiles read
	// access) would re-hit the real API on every Active user instead of once per sync —
	// doubling that user's calls (the failed lookup, then the GetUserDetails fallback)
	// against the same hourly budget this fix exists to protect.
	//
	// A genuinely persistent failure (see isCacheablePermissionProfilesError's doc) is
	// cached immediately. A transient-shaped failure (a plain 5xx/network blip, or any
	// other unclassified error) is deliberately NOT cached on the first attempt —
	// caching it would replay a stale error forever instead of ever re-checking whether
	// the condition cleared — but permissionProfilesTransientFails bounds the resulting
	// worst case: after permissionProfilesTransientFailureThreshold consecutive
	// transient failures of THAT kind, the call is treated as a sustained outage rather
	// than a blip and cached anyway, so the 2-calls-per-user cost (failed lookup +
	// GetUserDetails fallback) only applies to the first few Active users in the sync,
	// not all of them — the rest fall back at the same 1-call-per-user cost this fast
	// path existed before. A reclassified rate-limit error and a context
	// cancellation/deadline are exempt from this counter entirely — see
	// getPermissionProfiles' doc for why counting either toward the threshold would be
	// actively harmful, not just a missed optimization.
	//
	// permissionProfilesRateLimitedUntil/permissionProfilesRateLimitedErr are a separate,
	// narrower guard than the permissionProfilesCached mechanism above: a reclassified
	// rate-limit error is deliberately never cached via permissionProfilesCached (see
	// getPermissionProfiles' doc — caching it would prevent ever re-checking whether the
	// hourly window has reset), but Grants() runs concurrently across every Active user,
	// so without this, each waiting goroutine would in turn make its own real HTTP call
	// the instant it acquires the mutex, hammering an already-exhausted hourly budget
	// with one wasted call per Active user within seconds. permissionProfilesRateLimitedUntil
	// bounds that: it's a short TTL (see permissionProfilesRateLimitTTL's doc) that
	// collapses a same-instant concurrent burst into a single real call while still
	// expiring well before the SDK's own ~60s per-action retry cadence, so a genuine later
	// retry always gets a fresh check rather than being blocked by a stale cached failure.
	permissionProfilesMu                sync.Mutex
	permissionProfilesSyncID            string
	permissionProfilesCached            bool
	permissionProfiles                  []client.PermissionProfile
	permissionProfilesErr               error
	permissionProfilesTransientFails    int
	permissionProfilesRateLimitedUntil  time.Time
	permissionProfilesRateLimitedErr    error
	permissionProfilesEmptySyncIDLogged bool
}

// permissionProfilesTransientFailureThreshold is how many consecutive transient
// getPermissionProfiles failures this builder tolerates (each retried against the real
// API) before treating the failure as a sustained outage and caching it anyway — see
// the memoization fields' doc on the struct above. Chosen to still give a genuine blip
// (a single dropped request, one 503 during a brief window) more than one chance to
// clear before falling back to the pre-fast-path 1-call-per-user cost for the rest of
// the sync.
const permissionProfilesTransientFailureThreshold = 3

// permissionProfilesRateLimitTTL bounds how long getPermissionProfiles short-circuits
// concurrent callers to a cached reclassified-rate-limit error (see
// permissionProfilesRateLimitedUntil's doc on the struct above) before allowing another
// real HTTP call. Grants() runs concurrently across every Active user in a sync, and the
// mutex alone only serializes access to this cache — it doesn't stop each waiting
// goroutine from, in turn, making its own real call the instant it acquires the lock.
// Without a TTL guard, a single rate-limited episode would cost one wasted call per
// Active user within seconds — the exact call-amplification this whole fix exists to
// prevent, just moved one level down.
//
// 5 seconds is chosen to be clearly, safely shorter than the SDK's own per-action retry
// cadence (this repo's prior investigation into pkg/retry/retry.go's MaxDelay clamp
// found it waits roughly 60s between attempts): the TTL only ever needs to be long enough
// to collapse calls that are part of the same instant (a burst of goroutines all waiting
// on the same mutex when the rate limit first hits), never long enough to still be in
// effect by the time a genuinely separate retry attempt comes around. That's what keeps
// this from reintroducing the exact problem isCacheablePermissionProfilesError's
// allowlist (and getPermissionProfiles' refusal to cache this error via
// permissionProfilesCached) exists to prevent: a short TTL that's already expired by the
// next real retry never blocks that retry from re-checking whether the hourly window has
// reset, it only ever suppresses calls that were always going to hit the same still-open
// window anyway.
const permissionProfilesRateLimitTTL = 5 * time.Second

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
// client.GetPermissionProfilesFresh at most once per sync (keyed by syncID — see the
// memoization fields' doc on the struct above for why this builder can't just trust the
// cache for its whole process lifetime) unless the call fails with a non-cacheable
// (transient) error — see isCacheablePermissionProfilesError's doc — and even then, only
// up to permissionProfilesTransientFailureThreshold consecutive times before that
// transient failure is cached too, bounding the worst-case call cost. Two exceptions
// never count toward that threshold and are never cached via permissionProfilesCached no
// matter how many times they recur:
//   - A reclassified rate-limit error: unlike an ordinary transient blip, this failure
//     has a known, bounded resolution (the hourly window resetting), so it's always
//     worth a real retry — caching it via permissionProfilesCached would replay the same
//     stale codes.Unavailable on every retry of the SDK's per-action retry loop
//     (unlimited attempts, same builder reused) forever, never re-checking whether the
//     window has actually reset. This is the exact regression the
//     isCacheablePermissionProfilesError allowlist already exists to prevent; the
//     threshold must not reintroduce it via a different path. It DOES get a much
//     shorter-lived TTL guard instead — see permissionProfilesRateLimitedUntil's doc — to
//     collapse a same-instant concurrent burst across Grants() calls without blocking a
//     genuine later retry.
//   - A context cancellation/deadline: only means whichever caller's context won this
//     attempt was already done, not that the endpoint is actually degraded. An unlucky
//     run of cancellations shouldn't accumulate toward disabling the fast path on an
//     otherwise-healthy account.
//
// The second return value carries GetPermissionProfilesFresh's rate-limit annotations,
// but only on the invocation that actually performed the HTTP round-trip this sync — the
// memo-hit early return (permissionProfilesCached) and the rate-limit-TTL early return
// (permissionProfilesRateLimitedUntil) both return nil annotations, since neither made a
// real request and forwarding a stale, reused annotation would misrepresent the current
// request's pacing to the SDK's self-throttling rate limiter.
func (b *userBuilder) getPermissionProfiles(ctx context.Context, syncID string) ([]client.PermissionProfile, annotations.Annotations, error) {
	if err := ctx.Err(); err != nil {
		// Fail fast on an already-done context instead of queuing behind
		// permissionProfilesMu: the lock is held across the real HTTP round-trip below
		// (see getPermissionProfiles' doc), so a goroutine whose context is already
		// cancelled/expired would otherwise wait out that entire call for nothing.
		return nil, nil, err
	}
	b.permissionProfilesMu.Lock()
	defer b.permissionProfilesMu.Unlock()

	if syncID == "" {
		// baton-sdk only threads a real SyncID through when its own version check
		// passes (see the struct doc above); when it doesn't, every call arrives with
		// syncID == "" and permissionProfilesSyncID's zero value is also "" — so the
		// mismatch check below would never fire again after the first cache write,
		// silently reverting to the exact process-lifetime memoization bug this
		// SyncID-keying was added to fix, just without ever saying so. Refuse to
		// memoize at all in that case instead: every call is a real one (the
		// pre-fast-path cost), which is correct if slower, and log it once per
		// process so the condition is observable rather than a silent regression.
		if !b.permissionProfilesEmptySyncIDLogged {
			b.permissionProfilesEmptySyncIDLogged = true
			ctxzap.Extract(ctx).Debug("baton-docusign: SyncOpAttrs.SyncID is empty, disabling the permission-profiles per-sync cache",
				zap.String("effect", "falls back to one real call per Active user instead of one per sync"))
		}
		return b.client.GetPermissionProfilesFresh(ctx)
	}

	if b.permissionProfilesCached && b.permissionProfilesSyncID == syncID {
		return b.permissionProfiles, nil, b.permissionProfilesErr
	}
	if b.permissionProfilesSyncID != syncID {
		// A new sync started (or this is the first call ever): the previous sync's
		// cached result/error, transient-failure count, and rate-limit TTL no longer
		// apply. Reset all of them so this sync gets its own full
		// permissionProfilesTransientFailureThreshold chances and its own rate-limit
		// window rather than inheriting state left over from a prior sync.
		b.permissionProfilesSyncID = syncID
		b.permissionProfilesCached = false
		b.permissionProfilesTransientFails = 0
		b.permissionProfilesRateLimitedUntil = time.Time{}
		b.permissionProfilesRateLimitedErr = nil
	}

	// A concurrent burst within this same sync already found the account rate-limited
	// very recently: collapse this call into that same episode rather than issuing
	// another real request that's overwhelmingly likely to hit the same still-open
	// window. See permissionProfilesRateLimitTTL's doc for why this can't block a
	// genuine later retry.
	if time.Now().Before(b.permissionProfilesRateLimitedUntil) {
		return nil, nil, b.permissionProfilesRateLimitedErr
	}

	profiles, freshAnnos, err := b.client.GetPermissionProfilesFresh(ctx)
	if err != nil && !isCacheablePermissionProfilesError(err) {
		if isReclassifiedRateLimitError(err) {
			b.permissionProfilesRateLimitedUntil = time.Now().Add(permissionProfilesRateLimitTTL)
			b.permissionProfilesRateLimitedErr = err
			return nil, freshAnnos, err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, freshAnnos, err
		}
		b.permissionProfilesTransientFails++
		if b.permissionProfilesTransientFails < permissionProfilesTransientFailureThreshold {
			return nil, freshAnnos, err
		}
	}

	b.permissionProfilesCached = true
	b.permissionProfiles = profiles
	b.permissionProfilesErr = err
	return profiles, freshAnnos, err
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
func (b *userBuilder) Grants(ctx context.Context, resource *v2.Resource, attrs rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	userID := resource.Id

	if newGrant, annos, err, handled := b.tryFastPathGrant(ctx, resource, userID, attrs.SyncID); handled {
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

	return []*v2.Grant{
		newPermissionProfileGrant(permissionProfileID, userID),
	}, &rs.SyncOpResults{Annotations: annos}, nil
}

// newPermissionProfileGrant builds the permission_profile grant for userID against
// permissionProfileID — the one shape Grants' fallback and both of tryFastPathGrant's
// resolution branches all construct.
func newPermissionProfileGrant(permissionProfileID string, userID *v2.ResourceId) *v2.Grant {
	return grant.NewGrant(
		&v2.Resource{Id: &v2.ResourceId{ResourceType: permissionProfilesResourceType.Id, Resource: permissionProfileID}},
		permissionProfileAssignedTag,
		userID,
	)
}

// tryFastPathGrant is Grants' fast path for an Active user, avoiding the per-user
// GetUserDetails call that contributes to DocuSign's hourly rate limit. It resolves the
// grant using the permission-profile NAME already captured on the resource's profile
// during List(), resolved to an ID via GetPermissionProfiles — one account-wide call for
// the whole sync, via getPermissionProfiles' own memoization on this builder (see its
// doc), not uhttp's GET cache: that cache never stores a non-2xx response, so relying on
// it alone would let a persistent failure (not just a rate limit — e.g. a service user
// lacking permission_profiles read access) re-hit the real API once per Active user
// instead of once per sync, the same 1:1 amplification as the GetUserDetails path this
// fast path exists to avoid.
//
// A once-considered alternative — trusting a PermissionProfileID field directly on the
// list-users response, if DocuSign's API included one — was removed: it was never
// confirmed against a live tenant to return the same effective profile ID GetUserDetails
// returns for that user (a group-inherited or account-default value could differ), and a
// wrong grant here is silent and undetectable by the sync itself. The name-based
// resolution below is the well-tested, confirmed-correct mechanism.
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
// Forwards getPermissionProfiles' annotations (its second return value) on success, when
// that call actually performed the HTTP round-trip this sync — getPermissionProfiles' own
// memoization already limits this builder to exactly one real call per sync (see its
// doc), so that single call's rate-limit snapshot is the freshest, most representative
// signal available for the Grants pass, unlike relying on stale data from a prior sync.
// On any invocation served from getPermissionProfiles' internal cache/TTL short-circuits
// instead, it returns nil annotations, and so does this method.
//
// Does NOT forward annotations on the propagated-rate-limit-error branch below, even
// though getPermissionProfiles may return non-nil ones there too: Grants() (this method's
// only caller) discards any annotations returned alongside a non-nil error
// (`return nil, nil, err`) — the SDK never sees them either way — and that failed call's
// rate-limit signal already reaches the SDK's retry loop through the error's own gRPC
// status details (the RateLimitDescription reclassifyRateLimitError attaches), not
// through annotations. Threading a value through that's guaranteed to be dropped one
// frame up would just be dead code.
func (b *userBuilder) tryFastPathGrant(ctx context.Context, resource *v2.Resource, userID *v2.ResourceId, syncID string) (*v2.Grant, annotations.Annotations, error, bool) {
	profile := rs.GetProfile(resource)

	userStatus, ok := rs.GetProfileStringValue(profile, profileFieldStatus)
	if !ok || userStatus != userStatusActive {
		return nil, nil, nil, false
	}

	name, ok := rs.GetProfileStringValue(profile, profileFieldPermission)
	if !ok || name == "" {
		return nil, nil, nil, false
	}

	profiles, ppAnnos, err := b.getPermissionProfiles(ctx, syncID)
	if err != nil {
		if isReclassifiedRateLimitError(err) {
			// Wrapped (not returned bare) so a log line downstream can tell this
			// originated in the fast-path grant resolution, not any other DocuSign call —
			// %w preserves the gRPC status (codes.Unavailable + RateLimitDescription)
			// through errors.As, which status.Code/status.FromError already rely on (see
			// reclassifyRateLimitError's identical use of errors.Join for the same reason).
			return nil, nil, fmt.Errorf("failed to resolve permission profile for user %s: %w", userID.Resource, err), true
		}
		ctxzap.Extract(ctx).Debug("baton-docusign: GetPermissionProfiles failed, falling back to per-user GetUserDetails for this Grants call",
			zap.String("user_id", userID.Resource), zap.Error(err))
		return nil, nil, nil, false
	}

	id, matches := permissionProfileIDByName(profiles, name)
	if matches != 1 {
		return nil, nil, nil, false
	}
	return newPermissionProfileGrant(id, userID), ppAnnos, nil, true
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
