package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

// rewriteTransport redirects every outgoing request to the given target host — this
// package has no shared eSignature mock server (unlike pkg/client/clmtest for CLM), so
// this small helper is duplicated locally rather than exported from pkg/client's own
// unexported test-only copy.
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

// newSigningGroupsTestClient builds a *client.Client wired to a mock server that only
// serves /oauth/userinfo. signingGroupBuilder.List()'s only failure path that matters
// here is ensureInitialized (called by GetSigningGroups before it ever reaches the
// signing-groups endpoint), so a full eSignature REST API mock isn't needed to exercise
// it — matching how the CLM builders' equivalent tests fail at CLM account discovery.
func newSigningGroupsTestClient(t *testing.T, userInfoStatus int) *client.Client {
	t.Helper()
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/userinfo" {
			if userInfoStatus != http.StatusOK {
				w.WriteHeader(userInfoStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.UserInfoResponse{
				Sub:  "test-user",
				Name: "Test User",
				Accounts: []client.AccountInfo{
					{AccountId: "acct-1", AccountName: "Test Account", IsDefault: true, BaseURI: mockServer.URL},
				},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/signing_groups") {
			// An empty {} body round-trips through SigningGroupResponse as zero
			// signing groups and no next page (getNextToken's EndPosition+1 <
			// TotalSetSize is 0+1 < 0, false) — enough to exercise the happy path
			// without a full pagination fixture.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(mockServer.Close)

	mockServerURL, err := url.Parse(mockServer.URL)
	if err != nil {
		t.Fatalf("parsing mock server URL: %v", err)
	}
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})

	return client.NewClient(context.Background(), false, tokenSource, "", "", wrapper)
}

// TestSigningGroupBuilder_List_FailsWhenUnavailable is a regression test for the
// fail-loud behavior change in 47f58c3: signing_group is gated behind the
// --include-signing-groups flag (connector.go), but that flag doesn't validate the
// account actually has the feature before letting an operator turn it on. List() must
// now propagate any error (here, a 401 from eSignature account discovery) instead of
// tolerating it and silently syncing zero signing groups.
func TestSigningGroupBuilder_List_FailsWhenUnavailable(t *testing.T) {
	c := newSigningGroupsTestClient(t, http.StatusUnauthorized)
	b := newSigningGroupBuilder(c)
	ctx := context.Background()

	resources, _, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err == nil {
		t.Fatal("expected List to fail when signing groups are unavailable, got nil error")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero resources on a hard failure, got %d", len(resources))
	}
}

// TestSigningGroupBuilder_List_Succeeds is a sanity check for
// newSigningGroupsTestClient itself: confirms the happy path (account discovery
// succeeds) reaches List()'s normal return, distinguishing a correctly-wired mock from
// the fail-loud test above passing only because everything errors regardless.
func TestSigningGroupBuilder_List_Succeeds(t *testing.T) {
	c := newSigningGroupsTestClient(t, http.StatusOK)
	b := newSigningGroupBuilder(c)
	ctx := context.Background()

	resources, res, err := b.List(ctx, nil, rs.SyncOpAttrs{PageToken: pagination.Token{Size: 10}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected a non-nil SyncOpResults")
	}
	if len(resources) != 0 {
		t.Errorf("expected zero signing groups from this bare mock (no signing-groups endpoint served), got %d: %+v", len(resources), resources)
	}
}
