package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// rewriteTransport rewrites all outgoing request URLs to the given target host —
// mirrors pkg/client/client_test.go's helper of the same name (test files aren't
// importable across packages, so this is a small, deliberate duplicate).
type rewriteTransport struct {
	target *url.URL
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// usersTestServer wires a *client.Client to a mock server handling /oauth/userinfo,
// GET permission_profiles, and GET users/{id} — everything userBuilder.Grants needs
// across both its fast path and its GetUserDetails fallback. If forcePermissionProfilesRateLimit
// is set, the permission_profiles endpoint returns DocuSign's real hourly-rate-limit body
// instead of the profiles list, regardless of what's in profiles.
func newUsersTestClient(t *testing.T, profiles []client.PermissionProfile, userDetails map[string]client.UserDetail, forcePermissionProfilesRateLimit bool) *client.Client {
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
			if forcePermissionProfilesRateLimit {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(client.ErrorResponse{
					ErrorCode:    "HOURLY_APIINVOCATION_LIMIT_EXCEEDED",
					ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
				})
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
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: &rewriteTransport{target: mockServerURL}})
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
	}, nil, false) // no user-details fixtures — a fallback call here would 404 and fail the test
	b := newUserBuilder(c)
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
	// path existed — this is the regression test for the review finding that the fast
	// path could otherwise grant a profile to a disabled/closed user the old code
	// would have correctly skipped.
	c := newUsersTestClient(t, []client.PermissionProfile{
		{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
	}, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: ""}, // matches "non-active users have no PP"
	}, false)
	b := newUserBuilder(c)
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
	}, false)
	b := newUserBuilder(c)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "A Since-Renamed Profile")

	grants, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(grants) != 1 || grants[0].Entitlement.Resource.Id.Resource != "pp-1" {
		t.Errorf("expected the GetUserDetails fallback to resolve pp-1, got %+v", grants)
	}
}

// TestUserBuilder_Grants_PropagatesRateLimitInsteadOfDoublingCalls is a regression test
// for a deep-code-review finding: if GetPermissionProfiles fails because the account is
// already rate-limited (the exact scenario Pylon #11445 is about), falling through to
// GetUserDetails would hit the identical limit and double the failing calls per active
// user instead of reducing them. Grants must propagate that error directly.
func TestUserBuilder_Grants_PropagatesRateLimitInsteadOfDoublingCalls(t *testing.T) {
	c := newUsersTestClient(t, nil, map[string]client.UserDetail{
		// If Grants incorrectly falls through to GetUserDetails, this fixture would let
		// it "succeed" and hide the bug — present specifically so the fallthrough case
		// would be caught if it fired instead of propagating.
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, true)
	b := newUserBuilder(c)
	resource := userResourceWithProfile(t, "user-1", userStatusActive, "DocuSign Admin")

	_, _, err := b.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected Grants to propagate the rate-limit error, got nil")
	}
	if got := grpcstatus.Code(err); got != codes.Unavailable {
		t.Fatalf("expected codes.Unavailable, got %v: %v", got, err)
	}
}

func TestUserBuilder_Grants_FallsBackWhenProfileFieldMissing(t *testing.T) {
	// An identity-only or otherwise profile-less resource must not panic or skip the
	// grant — it should behave exactly as it did before the fast path existed.
	c := newUsersTestClient(t, []client.PermissionProfile{
		{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
	}, map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileID: "pp-1"},
	}, false)
	b := newUserBuilder(c)
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
