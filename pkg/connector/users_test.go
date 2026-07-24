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
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// rewriteTransport rewrites all outgoing request URLs to the given target host,
// mirroring the helper in pkg/client/client_test.go so requests issued against
// the real DocuSign hosts (oauth userinfo, account base URI) land on the mock
// server instead.
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

		switch {
		case r.URL.Path == "/oauth/userinfo":
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
		case r.URL.Path == "/restapi/v2.1/accounts/"+accountId+"/users/"+userId:
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

	c := client.NewClient(context.Background(), false, testTokenSource, "", testHTTPWrapper)

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
// permission_profile is included in the sync (syncPermissionProfiles=true, the
// default/no-filter behavior), Grants() still emits the cross-type
// permission_profile grant exactly as before this change.
func TestUserBuilder_Grants_SyncPermissionProfilesEnabled(t *testing.T) {
	ctx := context.Background()

	c, mockServer, callCount := newTestUserClient(t, client.UserDetail{
		UserID:              "user-1",
		PermissionProfileID: "pp-123",
	})
	defer mockServer.Close()

	b := newUserBuilder(c, true)

	grants, _, err := b.Grants(ctx, testUserResource(), resource.SyncOpAttrs{})
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
// verifies that Grants() itself no longer guards on syncPermissionProfiles:
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

	// syncPermissionProfiles=false here on purpose: Grants() must still emit
	// the grant, proving the old in-Grants() guard is gone.
	b := newUserBuilder(c, false)

	grants, _, err := b.Grants(ctx, testUserResource(), resource.SyncOpAttrs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(grants) != 1 {
		t.Fatalf("expected exactly 1 grant even with syncPermissionProfiles=false, got %d", len(grants))
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
	b := newUserBuilder(nil, true)

	rt := b.ResourceType(ctx)

	rtAnnos := annotations.Annotations(rt.Annotations)
	if !rtAnnos.Contains(&v2.SkipEntitlements{}) {
		t.Errorf("expected ResourceType() annotations to contain SkipEntitlements when syncPermissionProfiles=true")
	}
	if rtAnnos.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Errorf("expected ResourceType() annotations NOT to contain SkipEntitlementsAndGrants when syncPermissionProfiles=true")
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
	b := newUserBuilder(nil, false)

	rt := b.ResourceType(ctx)

	rtAnnos := annotations.Annotations(rt.Annotations)
	if !rtAnnos.Contains(&v2.SkipEntitlementsAndGrants{}) {
		t.Errorf("expected ResourceType() annotations to contain SkipEntitlementsAndGrants when syncPermissionProfiles=false")
	}
	if rtAnnos.Contains(&v2.SkipEntitlements{}) {
		t.Errorf("expected ResourceType() annotations NOT to contain a bare SkipEntitlements when syncPermissionProfiles=false")
	}

	if len(userResourceType.Annotations) != 0 {
		t.Errorf("expected package-level userResourceType.Annotations to remain empty after ResourceType() call, got %d entries",
			len(userResourceType.Annotations))
	}
}
