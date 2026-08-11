package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestReclassifyHourlyRateLimitError: DocuSign signals "hourly API-call budget exhausted"
// via a JSON error body (errorCode HOURLY_APIINVOCATION_LIMIT_EXCEEDED) on HTTP 400,
// which uhttp.GrpcCodeFromHTTPStatus maps to codes.InvalidArgument — a code the SDK's
// sync-retry loop treats as fatal, not retryable, so a full sync fails outright instead
// of pausing and resuming. reclassifyHourlyRateLimitError must re-classify exactly this
// case as codes.Unavailable (which the SDK does retry) carrying a RateLimitDescription,
// and leave every other error (including CLM's distinct error envelope) untouched.
func TestReclassifyHourlyRateLimitError(t *testing.T) {
	origErr := errors.New("400 Bad Request")

	t.Run("matches on errorCode and always uses the fixed hourly window", func(t *testing.T) {
		// The function takes no status code or headers at all — it can only ever see
		// errTarget's parsed body, so there's nothing header-derived to test here beyond
		// confirming the fixed window is what gets used.
		errTarget := &ErrorResponse{
			ErrorCode:    docusignHourlyRateLimitErrorCode,
			ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
		}

		got := reclassifyHourlyRateLimitError(errTarget, origErr)
		if got == nil {
			t.Fatal("expected a rate-limit error, got nil")
		}
		st, ok := status.FromError(got)
		if !ok {
			t.Fatalf("expected a gRPC status error, got %v", got)
		}
		if st.Code() != codes.Unavailable {
			t.Errorf("expected codes.Unavailable, got %v", st.Code())
		}

		var desc *v2.RateLimitDescription
		for _, d := range st.Details() {
			if rl, ok := d.(*v2.RateLimitDescription); ok {
				desc = rl
			}
		}
		if desc == nil {
			t.Fatalf("expected a RateLimitDescription in the error's status details, got %+v", st.Details())
		}
		if desc.GetStatus() != v2.RateLimitDescription_STATUS_OVERLIMIT {
			t.Errorf("expected STATUS_OVERLIMIT, got %v", desc.GetStatus())
		}
		if desc.GetRemaining() != 0 {
			t.Errorf("expected Remaining to be 0 (matches OVERLIMIT, no header data is ever read), got %d", desc.GetRemaining())
		}
		if desc.GetResetAt() == nil || desc.GetResetAt().AsTime().Before(time.Now().Add(50*time.Minute)) {
			t.Errorf("expected a ResetAt roughly an hour out, got %v", desc.GetResetAt())
		}
	})

	t.Run("does not match an unrelated errorCode", func(t *testing.T) {
		errTarget := &ErrorResponse{ErrorCode: "USER_LACKS_PERMISSIONS"}

		if got := reclassifyHourlyRateLimitError(errTarget, origErr); got != nil {
			t.Errorf("expected nil for an unrelated errorCode, got %v", got)
		}
	})

	t.Run("does not match CLM's distinct error envelope", func(t *testing.T) {
		// ClmErrorResponse is a different type from *ErrorResponse even if some CLM error
		// happened to carry the same string in an analogous field — the type assertion
		// alone must reject it, since this function's evidence is eSignature-specific.
		errTarget := &ClmErrorResponse{}

		if got := reclassifyHourlyRateLimitError(errTarget, origErr); got != nil {
			t.Errorf("expected nil for a non-eSignature error envelope, got %v", got)
		}
	})
}

// TestGetUsers_ClassifiesHourlyRateLimitAsRetryable is an end-to-end regression test,
// exercising the real request path (GetUsers -> doRequestCommon ->
// reclassifyHourlyRateLimitError) against a mock server that returns DocuSign's actual
// observed 400 body, rather than calling reclassifyHourlyRateLimitError directly.
func TestGetUsers_ClassifiesHourlyRateLimitAsRetryable(t *testing.T) {
	mockServer := httptest.NewServer(nil)
	defer mockServer.Close()

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oauth/userinfo" {
			_ = json.NewEncoder(w).Encode(UserInfoResponse{
				Sub: "service-account-user-id",
				Accounts: []AccountInfo{
					{AccountId: "acct-1", AccountName: "Acme", BaseURI: mockServer.URL, IsDefault: true},
				},
			})
			return
		}
		// GetUsers -> /restapi/v2.1/accounts/{id}/users
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			ErrorCode:    docusignHourlyRateLimitErrorCode,
			ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
		})
	})

	mockServerURL, _ := url.Parse(mockServer.URL)
	transport := &rewriteTransport{target: mockServerURL, base: http.DefaultTransport}
	wrapper := uhttp.NewBaseHttpClient(&http.Client{Transport: transport})
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	c := NewClient(context.Background(), false, tokenSource, "", "", wrapper)

	_, _, _, err := c.GetUsers(context.Background(), PageOptions{})
	if err == nil {
		t.Fatal("expected GetUsers to surface the hourly rate-limit error, got nil")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("expected codes.Unavailable (retryable by the SDK's sync-retry loop), got %v: %v", got, err)
	}
	st, _ := status.FromError(err)
	var desc *v2.RateLimitDescription
	for _, d := range st.Details() {
		if rl, ok := d.(*v2.RateLimitDescription); ok {
			desc = rl
		}
	}
	if desc == nil {
		t.Fatalf("expected the error to carry a RateLimitDescription, got details: %+v", st.Details())
	}
	if desc.GetStatus() != v2.RateLimitDescription_STATUS_OVERLIMIT {
		t.Errorf("expected STATUS_OVERLIMIT, got %v", desc.GetStatus())
	}
}
