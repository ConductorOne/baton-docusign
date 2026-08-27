package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/oauth2"
)

// recordedUpdates collects UpdateUserProfileRequest bodies from the mock PUT handler.
// Appends happen on the httptest goroutine; Snapshot is called from the test goroutine —
// the mutex gives a happens-before edge so go test -race stays clean.
type recordedUpdates struct {
	mu   sync.Mutex
	reqs []client.UpdateUserProfileRequest
}

func (r *recordedUpdates) append(req client.UpdateUserProfileRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
}

func (r *recordedUpdates) Snapshot() []client.UpdateUserProfileRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]client.UpdateUserProfileRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// newPermissionProfilesTestClient wires a *client.Client to a mock server handling
// /oauth/userinfo, GET permission_profiles, GET users/{id}, and PUT users/{id}/profile —
// everything permissionProfilesBuilder.Revoke needs. updateCalls, if non-nil, records
// every PUT users/{id}/profile body so tests can assert which profile ID Revoke assigned.
func newPermissionProfilesTestClient(
	t *testing.T,
	profiles []client.PermissionProfile,
	userDetails map[string]client.UserDetail,
	updateCalls *recordedUpdates,
) *client.Client {
	t.Helper()
	mockServer := httptest.NewServer(nil)
	t.Cleanup(mockServer.Close)

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/oauth/userinfo":
			_ = json.NewEncoder(w).Encode(client.UserInfoResponse{
				Sub: "service-account-user-id",
				Accounts: []client.AccountInfo{
					{AccountId: "acct-1", AccountName: "Acme", BaseURI: mockServer.URL, IsDefault: true},
				},
			})
		case r.URL.Path == "/restapi/v2.1/accounts/acct-1/permission_profiles":
			_ = json.NewEncoder(w).Encode(client.PermissionProfilesResponse{PermissionProfiles: profiles})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/profile"):
			var req client.UpdateUserProfileRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if updateCalls != nil {
				updateCalls.append(req)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{})
		case r.Method == http.MethodGet:
			const prefix = "/restapi/v2.1/accounts/acct-1/users/"
			if strings.HasPrefix(r.URL.Path, prefix) {
				userID := strings.TrimPrefix(r.URL.Path, prefix)
				if detail, ok := userDetails[userID]; ok {
					_ = json.NewEncoder(w).Encode(detail)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	mockServerURL, err := url.Parse(mockServer.URL)
	if err != nil {
		t.Fatalf("failed to parse mock server URL: %v", err)
	}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}})
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	return client.NewClient(context.Background(), false, tokenSource, "", "", wrapper)
}

func revokeGrantFor(userID, permissionProfileID string) *v2.Grant {
	return grant.NewGrant(
		&v2.Resource{Id: &v2.ResourceId{ResourceType: permissionProfilesResourceType.Id, Resource: permissionProfileID}},
		permissionProfileAssignedTag,
		&v2.ResourceId{ResourceType: userResourceType.Id, Resource: userID},
	)
}

// TestPermissionProfilesBuilder_Revoke_AmbiguousDefaultNameUsesFirstMatch is a
// regression test restoring Revoke's pre-existing behavior: DocuSign does not enforce
// unique permission profile names, so more than one profile can share
// defaultPermissionProfileName ("DocuSign Viewer"). Revoke must not hard-error in that
// case — it must take the first match (same order the API returned them) and succeed,
// logging (at Debug — this repo doesn't use Warn) rather than silently guessing. Without
// this, an account with a duplicate-named default profile could never have
// permission-profile grants revoked through this connector at all.
func TestPermissionProfilesBuilder_Revoke_AmbiguousDefaultNameUsesFirstMatch(t *testing.T) {
	ctx := context.Background()

	profiles := []client.PermissionProfile{
		{PermissionProfileId: "pp-viewer-first", PermissionProfileName: defaultPermissionProfileName},
		{PermissionProfileId: "pp-viewer-second", PermissionProfileName: defaultPermissionProfileName},
		{PermissionProfileId: "pp-admin", PermissionProfileName: "DocuSign Admin"},
	}
	userDetails := map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileName: "DocuSign Admin", PermissionProfileID: "pp-admin"},
	}

	var updateCalls recordedUpdates
	c := newPermissionProfilesTestClient(t, profiles, userDetails, &updateCalls)
	b := newPermissionProfilesBuilder(c)

	core, logs := observer.New(zapcore.DebugLevel)
	observedCtx := ctxzap.ToContext(ctx, zap.New(core))

	annos, err := b.Revoke(observedCtx, revokeGrantFor("user-1", "pp-admin"))
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	_ = annos

	gotCalls := updateCalls.Snapshot()
	if len(gotCalls) != 1 {
		t.Fatalf("expected exactly 1 UpdateUserProfile call, got %d", len(gotCalls))
	}
	if got := gotCalls[0].UserDetails.PermissionProfileId; got != "pp-viewer-first" {
		t.Errorf("expected Revoke to assign the first-listed match %q, got %q", "pp-viewer-first", got)
	}

	debugLogs := logs.FilterMessageSnippet("ambiguous")
	if debugLogs.Len() != 1 {
		t.Fatalf("expected exactly 1 Debug log about the ambiguous default profile name, got %d", debugLogs.Len())
	}
}

// TestPermissionProfilesBuilder_Revoke_UnambiguousDefaultNameNoWarning is the control
// case for the test above: a single unambiguous default profile must revoke exactly as
// before, with no ambiguous-match log at all.
func TestPermissionProfilesBuilder_Revoke_UnambiguousDefaultNameNoWarning(t *testing.T) {
	ctx := context.Background()

	profiles := []client.PermissionProfile{
		{PermissionProfileId: "pp-viewer", PermissionProfileName: defaultPermissionProfileName},
		{PermissionProfileId: "pp-admin", PermissionProfileName: "DocuSign Admin"},
	}
	userDetails := map[string]client.UserDetail{
		"user-1": {UserID: "user-1", PermissionProfileName: "DocuSign Admin", PermissionProfileID: "pp-admin"},
	}

	var updateCalls recordedUpdates
	c := newPermissionProfilesTestClient(t, profiles, userDetails, &updateCalls)
	b := newPermissionProfilesBuilder(c)

	core, logs := observer.New(zapcore.DebugLevel)
	observedCtx := ctxzap.ToContext(ctx, zap.New(core))

	if _, err := b.Revoke(observedCtx, revokeGrantFor("user-1", "pp-admin")); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	gotCalls := updateCalls.Snapshot()
	if len(gotCalls) != 1 || gotCalls[0].UserDetails.PermissionProfileId != "pp-viewer" {
		t.Fatalf("expected Revoke to assign pp-viewer, got %+v", gotCalls)
	}
	if logs.FilterMessageSnippet("ambiguous").Len() != 0 {
		t.Errorf("expected no ambiguous-match logs for an unambiguous default profile name, got %d", logs.FilterMessageSnippet("ambiguous").Len())
	}
}

// TestPermissionProfilesBuilder_Revoke_NoDefaultProfileStillErrors makes sure the
// restored first-match behavior didn't accidentally weaken the genuine not-found case:
// zero matches must still error, not silently no-op.
func TestPermissionProfilesBuilder_Revoke_NoDefaultProfileStillErrors(t *testing.T) {
	ctx := context.Background()

	profiles := []client.PermissionProfile{
		{PermissionProfileId: "pp-admin", PermissionProfileName: "DocuSign Admin"},
	}
	c := newPermissionProfilesTestClient(t, profiles, nil, nil)
	b := newPermissionProfilesBuilder(c)

	_, err := b.Revoke(ctx, revokeGrantFor("user-1", "pp-admin"))
	if err == nil {
		t.Fatal("expected an error when the default permission profile is missing entirely, got nil")
	}
}
