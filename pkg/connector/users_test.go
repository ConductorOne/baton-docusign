package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// rewriteTransport rewrites all outgoing request URLs to the given target host,
// mirroring the helper in pkg/client/client_test.go so requests issued against
// the real DocuSign hosts (oauth userinfo, account base URI) land on the mock
// server instead. base is injectable (rather than hardcoding
// http.DefaultTransport) so callers can wrap/observe the underlying transport.
type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return t.base.RoundTrip(req)
}

// newTestUserClient wires up a *client.Client against an httptest.Server that
// serves the OAuth userinfo endpoint and the GetUserDetails endpoint
// (GET /restapi/v2.1/accounts/{accountId}/users/{userId}). It returns the
// client, the server (caller must Close it), and a pointer to a counter that
// is incremented on every call to the GetUserDetails endpoint so tests can
// assert whether the API was actually hit.
func newTestUserClient(t *testing.T, userDetail client.UserDetail) (*client.Client, *httptest.Server, *int32) {
	t.Helper()

	const accountId = "acct-1"
	const userId = "user-1"

	var getUserDetailsCalls int32

	mockServer := httptest.NewServer(nil)
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/oauth/userinfo":
			_ = json.NewEncoder(w).Encode(client.UserInfoResponse{
				Sub:   "service-account-user-id",
				Name:  "ConductorOne Service Account",
				Email: "c1-service@example.com",
				Accounts: []client.AccountInfo{
					{
						AccountId:   accountId,
						AccountName: "Acme",
						BaseURI:     mockServer.URL,
						IsDefault:   true,
					},
				},
			})
			return
		case "/restapi/v2.1/accounts/" + accountId + "/users/" + userId:
			atomic.AddInt32(&getUserDetailsCalls, 1)
			_ = json.NewEncoder(w).Encode(userDetail)
			return
		default:
			http.NotFound(w, r)
		}
	})

	mockServerURL, err := url.Parse(mockServer.URL)
	if err != nil {
		t.Fatalf("failed to parse mock server URL: %v", err)
	}
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	testHTTPWrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})
	testTokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})

	c := client.NewClient(context.Background(), false, testTokenSource, "", "", testHTTPWrapper)

	return c, mockServer, &getUserDetailsCalls
}

func testUserResource() *v2.Resource {
	return &v2.Resource{
		Id: &v2.ResourceId{
			ResourceType: userResourceType.Id,
			Resource:     "user-1",
		},
	}
}

// TestUserBuilder_Grants_SyncPermissionProfilesEnabled verifies that when
// permission_profile is included in the sync (skipPermissionProfileResourceType=false, the
// default/no-filter behavior), Grants() still emits the cross-type
// permission_profile grant exactly as before this change.
func TestUserBuilder_Grants_SyncPermissionProfilesEnabled(t *testing.T) {
	ctx := context.Background()

	c, mockServer, callCount := newTestUserClient(t, client.UserDetail{
		UserID:              "user-1",
		PermissionProfileID: "pp-123",
	})
	defer mockServer.Close()

	b := newUserBuilder(c, false) // permission_profile in scope

	grants, _, err := b.Grants(ctx, testUserResource(), rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(grants) != 1 {
		t.Fatalf("expected exactly 1 grant, got %d", len(grants))
	}

	got := grants[0]
	if got.Entitlement.Resource.Id.ResourceType != permissionProfilesResourceType.Id {
		t.Errorf("expected grant to reference resource type %q, got %q",
			permissionProfilesResourceType.Id, got.Entitlement.Resource.Id.ResourceType)
	}
	if got.Entitlement.Resource.Id.Resource != "pp-123" {
		t.Errorf("expected grant to reference permission profile %q, got %q",
			"pp-123", got.Entitlement.Resource.Id.Resource)
	}
	if got.Principal.Id.Resource != "user-1" {
		t.Errorf("expected grant principal to be user %q, got %q", "user-1", got.Principal.Id.Resource)
	}

	if atomic.LoadInt32(callCount) != 1 {
		t.Errorf("expected GetUserDetails to be called exactly once, got %d", atomic.LoadInt32(callCount))
	}
}

// TestUserBuilder_Grants_UnconditionalRegardlessOfSyncPermissionProfiles
// verifies that Grants() itself no longer guards on skipPermissionProfileResourceType:
// it always fetches user details and emits the cross-type permission_profile
// grant. The case where permission_profile is filtered out of sync is now
// handled declaratively via the ResourceType() annotation (see
// TestUserBuilder_ResourceType_SyncPermissionProfilesDisabled), which causes
// the SDK sync engine to skip calling Grants() at all — not by Grants()
// itself returning early.
func TestUserBuilder_Grants_UnconditionalRegardlessOfSyncPermissionProfiles(t *testing.T) {
	ctx := context.Background()

	c, mockServer, callCount := newTestUserClient(t, client.UserDetail{
		UserID:              "user-1",
		PermissionProfileID: "pp-123",
	})
	defer mockServer.Close()

	// skipPermissionProfileResourceType=true here on purpose: Grants() must still emit
	// the grant, proving the old in-Grants() guard is gone.
	b := newUserBuilder(c, true)

	grants, _, err := b.Grants(ctx, testUserResource(), rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(grants) != 1 {
		t.Fatalf("expected exactly 1 grant even with skipPermissionProfileResourceType=true, got %d", len(grants))
	}

	got := grants[0]
	if got.Entitlement.Resource.Id.ResourceType != permissionProfilesResourceType.Id {
		t.Errorf("expected grant to reference resource type %q, got %q",
			permissionProfilesResourceType.Id, got.Entitlement.Resource.Id.ResourceType)
	}
	if got.Entitlement.Resource.Id.Resource != "pp-123" {
		t.Errorf("expected grant to reference permission profile %q, got %q",
			"pp-123", got.Entitlement.Resource.Id.Resource)
	}

	if atomic.LoadInt32(callCount) != 1 {
		t.Errorf("expected GetUserDetails to be called exactly once, got %d", atomic.LoadInt32(callCount))
	}
}

// TestUserBuilder_ResourceType_SyncPermissionProfilesEnabled verifies that
// when permission_profile is included in the sync, ResourceType() attaches
// SkipEntitlements (Entitlements() is a no-op so it's safe to skip) but NOT
// SkipEntitlementsAndGrants (Grants() must still run to emit the cross-type
// permission_profile grant).
func TestUserBuilder_ResourceType_SyncPermissionProfilesEnabled(t *testing.T) {
	ctx := context.Background()
	b := newUserBuilder(nil, false) // permission_profile in scope

	rt := b.ResourceType(ctx)

	rtAnnos := annotations.Annotations(rt.Annotations)
	if !rtAnnos.Contains(&v2.SkipEntitlements{}) {
		t.Errorf("expected ResourceType() annotations to contain SkipEntitlements when skipPermissionProfileResourceType=false")
	}
	if rtAnnos.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Errorf("expected ResourceType() annotations NOT to contain SkipEntitlementsAndGrants when skipPermissionProfileResourceType=false")
	}
}

// TestUserBuilder_ResourceType_SyncPermissionProfilesDisabled verifies that
// when the customer's sync filter excludes permission_profile, ResourceType()
// attaches SkipEntitlementsAndGrants (so the SDK sync engine skips calling
// both Entitlements() and Grants() for user resources entirely) but NOT a
// bare SkipEntitlements. It also verifies that building this annotated
// ResourceType does not mutate the shared package-level userResourceType var.
func TestUserBuilder_ResourceType_SyncPermissionProfilesDisabled(t *testing.T) {
	ctx := context.Background()
	b := newUserBuilder(nil, true) // permission_profile filtered out

	rt := b.ResourceType(ctx)

	rtAnnos := annotations.Annotations(rt.Annotations)
	if !rtAnnos.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Errorf("expected ResourceType() annotations to contain SkipEntitlementsAndGrants when skipPermissionProfileResourceType=true")
	}
	if rtAnnos.Contains(&v2.SkipEntitlements{}) {
		t.Errorf("expected ResourceType() annotations NOT to contain a bare SkipEntitlements when skipPermissionProfileResourceType=true")
	}

	if len(userResourceType.Annotations) != 0 {
		t.Errorf("expected package-level userResourceType.Annotations to remain empty after ResourceType() call, got %d entries",
			len(userResourceType.Annotations))
	}
}

// Forced-error modes for newUsersTestClient's permission_profiles endpoint.
const (
	permissionProfilesOK                 = ""
	permissionProfilesRateLimit          = "rate_limit"          // DocuSign's real hourly-limit body
	permissionProfilesForbidden          = "forbidden"           // a generic, unrelated failure
	permissionProfilesServiceUnavailable = "service_unavailable" // a plain 503, no rate-limit body at all
)

// newUsersTestClient wires a *client.Client to a mock server handling /oauth/userinfo,
// GET permission_profiles, and GET users/{id} — everything userBuilder.Grants needs
// across both its fast path and its GetUserDetails fallback. forcedPermissionProfilesError
// selects what the permission_profiles endpoint returns instead of the profiles list —
// see the permissionProfiles* constants above.
func newUsersTestClient(t *testing.T, profiles []client.PermissionProfile, userDetails map[string]client.UserDetail, forcedPermissionProfilesError string) *client.Client {
	t.Helper()
	mockServer := httptest.NewServer(nil)
	t.Cleanup(mockServer.Close)

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/oauth/userinfo":
			_ = json.NewEncoder(w).Encode(client.UserInfoResponse{
				Sub: "service-account-user-id",
				Accounts: []client.AccountInfo{
					{AccountId: "acct-1", AccountName: "Acme", BaseURI: mockServer.URL, IsDefault: true},
				},
			})
		case "/restapi/v2.1/accounts/acct-1/permission_profiles":
			switch forcedPermissionProfilesError {
			case permissionProfilesRateLimit:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(client.ErrorResponse{
					ErrorCode:    "HOURLY_APIINVOCATION_LIMIT_EXCEEDED",
					ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
				})
				return
			case permissionProfilesForbidden:
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(client.ErrorResponse{
					ErrorCode:    "USER_LACKS_PERMISSIONS",
					ErrorMessage: "The user does not have permission to access permission profiles.",
				})
				return
			case permissionProfilesServiceUnavailable:
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(client.ErrorResponse{ErrorMessage: "service unavailable"})
				return
			}
			_ = json.NewEncoder(w).Encode(client.PermissionProfilesResponse{PermissionProfiles: profiles})
		default:
			// GET .../users/{id}
			const prefix = "/restapi/v2.1/accounts/acct-1/users/"
			if len(r.URL.Path) > len(prefix) && r.URL.Path[:len(prefix)] == prefix {
				userID := r.URL.Path[len(prefix):]
				if detail, ok := userDetails[userID]; ok {
					_ = json.NewEncoder(w).Encode(detail)
					return
				}
			}
			http.NotFound(w, r)
		}
	})

	mockServerURL, _ := url.Parse(mockServer.URL)
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}})
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	return client.NewClient(context.Background(), false, tokenSource, "", "", wrapper)
}

func userResourceWithProfile(t *testing.T, id, status, permission string) *v2.Resource {
	t.Helper()
	res, err := rs.NewUserResource(id, userResourceType, id, nil, rs.WithResourceProfile(map[string]any{
		profileFieldStatus:     status,
		profileFieldPermission: permission,
	}))
	if err != nil {
		t.Fatalf("NewUserResource: %v", err)
	}
	return res
}

func TestUserBuilder_Grants_FastPath_ActiveUserWithKnownProfile(t *testing.T) {
	c := newUsersTestClient(t, []client.PermissionProfile{
		{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
		{PermissionProfileId: "pp-2", PermissionProfileName: "DocuSign Viewer"},
	}, nil, permissionProfilesOK) // no user-details fixtures — a fallback call here would 404 and fail the test
	b := newUserBuilder(c, false)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "DocuSign Admin")

	grants, res, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil SyncOpResults")
	}
	if len(grants) != 1 {
		t.Fatalf("expected exactly 1 grant, got %d: %+v", len(grants), grants)
	}
	if got := grants[0].Entitlement.Resource.Id.Resource; got != "pp-1" {
		t.Errorf("expected grant against permission profile pp-1, got %s", got)
	}
}

func TestUserBuilder_Grants_FallsBackWhenNotActive(t *testing.T) {
	// A non-active user must go through GetUserDetails, exactly like before this fast
	// path existed — otherwise the fast path could grant a profile to a disabled/closed
	// user that GetUserDetails would correctly skip.
	c := newUsersTestClient(t, []client.PermissionProfile{
		{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
	}, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: ""}, // matches "non-active users have no PP"
	}, permissionProfilesOK)
	b := newUserBuilder(c, false)
	resource := userResourceWithProfile(t, "user-1", "Disabled", "DocuSign Admin")

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("expected zero grants for a disabled user, got %d: %+v", len(grants), grants)
	}
}

func TestUserBuilder_Grants_FallsBackWhenProfileNameUnresolvable(t *testing.T) {
	// The cached profile name no longer matches any current permission profile (e.g.
	// renamed/deleted since this user was listed) — must fall back, not just drop the
	// grant silently.
	c := newUsersTestClient(t, []client.PermissionProfile{
		{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
	}, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, permissionProfilesOK)
	b := newUserBuilder(c, false)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "A Since-Renamed Profile")

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 || grants[0].Entitlement.Resource.Id.Resource != "pp-1" {
		t.Errorf("expected the GetUserDetails fallback to resolve pp-1, got %+v", grants)
	}
}

// TestUserBuilder_Grants_PropagatesRateLimitInsteadOfDoublingCalls: if
// GetPermissionProfiles fails because the account is already rate-limited, falling
// through to GetUserDetails would hit the identical limit and double the failing calls
// per active user instead of reducing them. Grants must propagate that error directly.
func TestUserBuilder_Grants_PropagatesRateLimitInsteadOfDoublingCalls(t *testing.T) {
	c := newUsersTestClient(t, nil, map[string]client.UserDetail{
		// If Grants incorrectly falls through to GetUserDetails, this fixture would let
		// it "succeed" and hide the bug — present specifically so the fallthrough case
		// would be caught if it fired instead of propagating.
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, permissionProfilesRateLimit)
	b := newUserBuilder(c, false)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "DocuSign Admin")

	_, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected Grants to propagate the rate-limit error, got nil")
	}
	if got := grpcstatus.Code(err); got != codes.Unavailable {
		t.Fatalf("expected codes.Unavailable, got %v: %v", got, err)
	}
}

// TestUserBuilder_Grants_FallsBackOnNonRateLimitPermissionProfilesFailure covers the
// other half of the fast path's error handling: a GetPermissionProfiles failure that
// ISN'T the reclassified rate-limit error (e.g. a permissions/scope issue) must still
// fall through to GetUserDetails and resolve the grant, not propagate the error.
func TestUserBuilder_Grants_FallsBackOnNonRateLimitPermissionProfilesFailure(t *testing.T) {
	c := newUsersTestClient(t, nil, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, permissionProfilesForbidden)
	b := newUserBuilder(c, false)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "DocuSign Admin")

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 || grants[0].Entitlement.Resource.Id.Resource != "pp-1" {
		t.Errorf("expected the GetUserDetails fallback to resolve pp-1, got %+v", grants)
	}
}

// TestUserBuilder_Grants_FallsBackOnServiceUnavailable: codes.Unavailable is broader
// than "already rate-limited" — uhttp also maps a plain HTTP 503 to it. A 503 from
// GetPermissionProfiles (no RateLimitDescription attached, unlike the reclassified
// rate-limit error) must fall through to GetUserDetails, not be mistaken for the
// rate-limit case and propagated.
func TestUserBuilder_Grants_FallsBackOnServiceUnavailable(t *testing.T) {
	c := newUsersTestClient(t, nil, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, permissionProfilesServiceUnavailable)
	b := newUserBuilder(c, false)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "DocuSign Admin")

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 || grants[0].Entitlement.Resource.Id.Resource != "pp-1" {
		t.Errorf("expected the GetUserDetails fallback to resolve pp-1, got %+v", grants)
	}
}

// TestUserBuilder_Grants_FastPath_PrefersDirectProfileIDOverName covers the
// PermissionProfileID-on-the-list-response path: when present, it must skip
// GetPermissionProfiles entirely and use the ID directly. If it fell through instead,
// GetPermissionProfiles would succeed with an empty list (no profiles fixture is
// provided), fail to resolve "DocuSign Admin" by name, and fall through again to
// GetUserDetails — which 404s (no userDetails fixture either) and fails this test.
func TestUserBuilder_Grants_FastPath_PrefersDirectProfileIDOverName(t *testing.T) {
	c := newUsersTestClient(t, nil, nil, permissionProfilesOK)
	b := newUserBuilder(c, false)
	resource, err := rs.NewUserResource("user-1", userResourceType, "user-1", nil, rs.WithResourceProfile(map[string]any{
		profileFieldStatus:       userStatusActive,
		profileFieldPermission:   "DocuSign Admin",
		profileFieldPermissionID: "pp-1",
	}))
	if err != nil {
		t.Fatalf("NewUserResource: %v", err)
	}

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 || grants[0].Entitlement.Resource.Id.Resource != "pp-1" {
		t.Errorf("expected the direct profile ID to resolve pp-1 with no API call, got %+v", grants)
	}
}

func TestUserBuilder_Grants_FallsBackWhenProfileFieldMissing(t *testing.T) {
	// An identity-only or otherwise profile-less resource must not panic or skip the
	// grant — it should behave exactly as it did before the fast path existed.
	c := newUsersTestClient(t, []client.PermissionProfile{
		{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
	}, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, permissionProfilesOK)
	b := newUserBuilder(c, false)
	resource, err := rs.NewUserResource("user-1", userResourceType, "user-1", nil)
	if err != nil {
		t.Fatalf("NewUserResource: %v", err)
	}

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 || grants[0].Entitlement.Resource.Id.Resource != "pp-1" {
		t.Errorf("expected the GetUserDetails fallback to resolve pp-1, got %+v", grants)
	}
}
