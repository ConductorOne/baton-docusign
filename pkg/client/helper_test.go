package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRateLimitErrorFromResponse is a regression test for Pylon #11445: DocuSign signals
// "hourly API-call budget exhausted" via a JSON error body (errorCode
// HOURLY_APIINVOCATION_LIMIT_EXCEEDED) on HTTP 400, which uhttp.GrpcCodeFromHTTPStatus maps
// to codes.InvalidArgument — a code the SDK's sync-retry loop treats as fatal, not
// retryable, so a real customer's initial full sync failed outright instead of pausing and
// resuming. rateLimitErrorFromResponse must re-classify exactly this case as
// codes.Unavailable (which the SDK does retry) carrying a RateLimitDescription, and leave
// every other error (including CLM's distinct error envelope) untouched.
func TestRateLimitErrorFromResponse(t *testing.T) {
	origErr := errors.New("400 Bad Request")

	t.Run("matches on errorCode regardless of HTTP status", func(t *testing.T) {
		for _, statusCode := range []int{http.StatusBadRequest, http.StatusTooManyRequests} {
			resp := &http.Response{
				StatusCode: statusCode,
				Header:     http.Header{},
			}
			errTarget := &ErrorResponse{
				ErrorCode:    docusignHourlyRateLimitErrorCode,
				ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
			}

			got := rateLimitErrorFromResponse(resp, errTarget, origErr)
			if got == nil {
				t.Fatalf("status %d: expected a rate-limit error, got nil", statusCode)
			}
			st, ok := status.FromError(got)
			if !ok {
				t.Fatalf("status %d: expected a gRPC status error, got %v", statusCode, got)
			}
			if st.Code() != codes.Unavailable {
				t.Errorf("status %d: expected codes.Unavailable, got %v", statusCode, st.Code())
			}

			var desc *v2.RateLimitDescription
			for _, d := range st.Details() {
				if rl, ok := d.(*v2.RateLimitDescription); ok {
					desc = rl
				}
			}
			if desc == nil {
				t.Fatalf("status %d: expected a RateLimitDescription in the error's status details, got %+v", statusCode, st.Details())
			}
			if desc.GetStatus() != v2.RateLimitDescription_STATUS_OVERLIMIT {
				t.Errorf("status %d: expected STATUS_OVERLIMIT, got %v", statusCode, desc.GetStatus())
			}
			if desc.GetResetAt() == nil || desc.GetResetAt().AsTime().Before(time.Now()) {
				t.Errorf("status %d: expected a future ResetAt when no header is present, got %v", statusCode, desc.GetResetAt())
			}
		}
	})

	t.Run("ignores rate-limit headers and always uses the fixed hourly window", func(t *testing.T) {
		// DocuSign documents no dedicated headers for this hourly/daily-scoped limit —
		// any generic X-RateLimit-*/Ratelimit-* headers present most plausibly describe
		// an unrelated shorter-window limit (e.g. a burst counter), not the hourly one
		// that produced this error. Trusting them would risk the SDK's Retryer computing
		// a too-short wait off the wrong bucket's Remaining and re-hitting an account
		// that's still over its hourly budget (a deep-code-review finding on this PR) —
		// so a header claiming an imminent reset must NOT shorten the wait below the
		// fixed default window.
		soonResetAt := time.Now().Add(5 * time.Minute)
		resp := &http.Response{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"X-Ratelimit-Reset":     []string{strconv.FormatInt(soonResetAt.Unix(), 10)},
				"X-Ratelimit-Remaining": []string{"5"},
			},
		}
		errTarget := &ErrorResponse{ErrorCode: docusignHourlyRateLimitErrorCode}

		got := rateLimitErrorFromResponse(resp, errTarget, origErr)
		if got == nil {
			t.Fatal("expected a rate-limit error, got nil")
		}
		st, _ := status.FromError(got)
		var desc *v2.RateLimitDescription
		for _, d := range st.Details() {
			if rl, ok := d.(*v2.RateLimitDescription); ok {
				desc = rl
			}
		}
		if desc == nil {
			t.Fatal("expected a RateLimitDescription in the error's status details")
		}
		if desc.GetRemaining() != 0 {
			t.Errorf("expected Remaining to stay 0 (header-derived value ignored), got %d", desc.GetRemaining())
		}
		if resetAt := desc.GetResetAt().AsTime(); resetAt.Before(soonResetAt.Add(time.Minute)) {
			t.Errorf("expected ResetAt to use the ~1h default window, not the header's near-term value: got %v", resetAt)
		}
	})

	t.Run("does not match an unrelated errorCode", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}
		errTarget := &ErrorResponse{ErrorCode: "USER_LACKS_PERMISSIONS"}

		if got := rateLimitErrorFromResponse(resp, errTarget, origErr); got != nil {
			t.Errorf("expected nil for an unrelated errorCode, got %v", got)
		}
	})

	t.Run("does not match CLM's distinct error envelope", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}
		// ClmErrorResponse is a different type from *ErrorResponse even if some CLM error
		// happened to carry the same string in an analogous field — the type assertion
		// alone must reject it, since this function's evidence is eSignature-specific.
		errTarget := &ClmErrorResponse{}

		if got := rateLimitErrorFromResponse(resp, errTarget, origErr); got != nil {
			t.Errorf("expected nil for a non-eSignature error envelope, got %v", got)
		}
	})
}

// TestGetUsers_ClassifiesHourlyRateLimitAsRetryable is an end-to-end regression test for
// Pylon #11445, exercising the real request path (GetUsers -> doRequestCommon ->
// rateLimitErrorFromResponse) against a mock server that returns DocuSign's actual
// observed 400 body, rather than calling rateLimitErrorFromResponse directly.
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
