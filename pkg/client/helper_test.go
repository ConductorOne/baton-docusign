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
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestReclassifyRateLimitError: DocuSign signals "hourly API-call budget exhausted" and
// "30-second burst limit exhausted" via a JSON error body (errorCode
// HOURLY_APIINVOCATION_LIMIT_EXCEEDED / BURST_APIINVOCATION_LIMIT_EXCEEDED) on HTTP 400,
// which uhttp.GrpcCodeFromHTTPStatus maps to codes.InvalidArgument — a code the SDK's
// sync-retry loop treats as fatal, not retryable, so a full sync fails outright instead
// of pausing and resuming. reclassifyRateLimitError must re-classify both cases as
// codes.Unavailable (which the SDK does retry) carrying a RateLimitDescription, and leave
// every other error (including CLM's distinct error envelope) untouched.
func TestReclassifyRateLimitError(t *testing.T) {
	origErr := errors.New("400 Bad Request")

	t.Run("matches on errorCode and always uses the fixed hourly window", func(t *testing.T) {
		// The function takes no status code or headers at all — it can only ever see
		// errTarget's parsed body, so there's nothing header-derived to test here beyond
		// confirming the fixed window is what gets used.
		errTarget := &ErrorResponse{
			ErrorCode:    docusignHourlyRateLimitErrorCode,
			ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
		}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
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

		if got := reclassifyRateLimitError(context.Background(), errTarget, origErr); got != nil {
			t.Errorf("expected nil for an unrelated errorCode, got %v", got)
		}
	})

	t.Run("matches on the docs-quoted message when errorCode differs", func(t *testing.T) {
		// Published rules-and-limits docs quote the message text for the account hourly
		// limit but do not list HOURLY_APIINVOCATION_LIMIT_EXCEEDED; accept either signal.
		errTarget := &ErrorResponse{
			ErrorCode:    "SOME_FUTURE_HOURLY_CODE",
			ErrorMessage: "The maximum number of hourly API invocations has been exceeded. The hourly limit is 3000.",
		}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
		if got == nil {
			t.Fatal("expected a rate-limit error from the docs-quoted message, got nil")
		}
		st, ok := status.FromError(got)
		if !ok {
			t.Fatalf("expected a gRPC status error, got %v", got)
		}
		if st.Code() != codes.Unavailable {
			t.Errorf("expected codes.Unavailable, got %v", st.Code())
		}
	})

	t.Run("matches errorCode case-insensitively", func(t *testing.T) {
		// DocuSign's own published error codes are inconsistent on casing across
		// endpoints (e.g. Hourly_APIInvocation_Envelope_Limit_Exceeded is mixed-case
		// where this connector's constant is all-caps) — a casing difference on the
		// exact account-level code (which isn't published at all) must still match.
		errTarget := &ErrorResponse{ErrorCode: "Hourly_APIInvocation_Limit_Exceeded"}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
		if got == nil {
			t.Fatal("expected a mixed-case errorCode to still match, got nil")
		}
		if st, _ := status.FromError(got); st.Code() != codes.Unavailable {
			t.Errorf("expected codes.Unavailable, got %v", st.Code())
		}
	})

	t.Run("matches message case-insensitively", func(t *testing.T) {
		errTarget := &ErrorResponse{
			ErrorCode:    "SOME_FUTURE_HOURLY_CODE",
			ErrorMessage: "THE MAXIMUM NUMBER OF HOURLY API INVOCATIONS HAS BEEN EXCEEDED. The hourly limit is 3000.",
		}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
		if got == nil {
			t.Fatal("expected a mixed-case message to still match, got nil")
		}
		if st, _ := status.FromError(got); st.Code() != codes.Unavailable {
			t.Errorf("expected codes.Unavailable, got %v", st.Code())
		}
	})

	t.Run("matches the burst-limit errorCode and uses the short burst window", func(t *testing.T) {
		errTarget := &ErrorResponse{
			ErrorCode:    docusignBurstRateLimitErrorCode,
			ErrorMessage: "You have exceeded the burst limit.",
		}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
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
		resetAt := desc.GetResetAt()
		if resetAt == nil {
			t.Fatal("expected a ResetAt")
		}
		wait := time.Until(resetAt.AsTime())
		if wait <= 30*time.Second || wait > time.Minute {
			t.Errorf("expected a ResetAt in the 30s-60s burst-window ballpark, got a wait of %v", wait)
		}
	})

	t.Run("matches the burst-limit errorCode case-insensitively", func(t *testing.T) {
		errTarget := &ErrorResponse{ErrorCode: "burst_apiinvocation_limit_exceeded"}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
		if got == nil {
			t.Fatal("expected a mixed-case burst errorCode to still match, got nil")
		}
		if st, _ := status.FromError(got); st.Code() != codes.Unavailable {
			t.Errorf("expected codes.Unavailable, got %v", st.Code())
		}
	})

	t.Run("hourly match takes priority and keeps the hourly window when both signals somehow appear", func(t *testing.T) {
		errTarget := &ErrorResponse{ErrorCode: docusignHourlyRateLimitErrorCode}

		got := reclassifyRateLimitError(context.Background(), errTarget, origErr)
		st, _ := status.FromError(got)
		var desc *v2.RateLimitDescription
		for _, d := range st.Details() {
			if rl, ok := d.(*v2.RateLimitDescription); ok {
				desc = rl
			}
		}
		if desc.GetResetAt() == nil || desc.GetResetAt().AsTime().Before(time.Now().Add(50*time.Minute)) {
			t.Errorf("expected the hourly window (~1h out), got %v", desc.GetResetAt())
		}
	})

	t.Run("does not match CLM's distinct error envelope", func(t *testing.T) {
		// ClmErrorResponse is a different type from *ErrorResponse even if some CLM error
		// happened to carry the same string in an analogous field — the type assertion
		// alone must reject it, since this function's evidence is eSignature-specific.
		errTarget := &ClmErrorResponse{}

		if got := reclassifyRateLimitError(context.Background(), errTarget, origErr); got != nil {
			t.Errorf("expected nil for a non-eSignature error envelope, got %v", got)
		}
	})

	t.Run("preserves origErr in the returned error's chain", func(t *testing.T) {
		// Regression test: reclassifyRateLimitError used to build a brand-new status
		// error from origErr.Error() (a string) and return only that, dropping the
		// original error VALUE — and anything joined into it by uhttp (e.g.
		// WrapErrorsWithRateLimitInfo's header-derived rate-limit data) — from the
		// chain entirely. A sentinel wrapped into origErr must still be reachable via
		// errors.Is/errors.As on the reclassified error, and status.Code/status.FromError
		// must still resolve to the new codes.Unavailable classification.
		sentinel := errors.New("sentinel: header-derived rate-limit detail")
		wrappedOrigErr := errors.Join(errors.New("400 Bad Request"), sentinel)

		errTarget := &ErrorResponse{ErrorCode: docusignHourlyRateLimitErrorCode}
		got := reclassifyRateLimitError(context.Background(), errTarget, wrappedOrigErr)

		if !errors.Is(got, sentinel) {
			t.Errorf("expected the reclassified error to still chain to the original sentinel error, got %v", got)
		}
		if status.Code(got) != codes.Unavailable {
			t.Errorf("expected status.Code to still resolve to codes.Unavailable after joining origErr, got %v", status.Code(got))
		}
		st, ok := status.FromError(got)
		if !ok {
			t.Fatalf("expected status.FromError to still find a gRPC status, got ok=false for %v", got)
		}
		var desc *v2.RateLimitDescription
		for _, d := range st.Details() {
			if rl, ok := d.(*v2.RateLimitDescription); ok {
				desc = rl
			}
		}
		if desc == nil {
			t.Errorf("expected the RateLimitDescription to still be reachable via status.FromError after joining origErr, got details: %+v", st.Details())
		}
	})

	t.Run("logs a breadcrumb identifying which rate-limit variant matched", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		ctx := ctxzap.ToContext(context.Background(), zap.New(core))

		errTarget := &ErrorResponse{ErrorCode: docusignBurstRateLimitErrorCode}
		if got := reclassifyRateLimitError(ctx, errTarget, origErr); got == nil {
			t.Fatal("expected a match")
		}

		entries := logs.All()
		if len(entries) != 1 {
			t.Fatalf("expected exactly 1 log entry on a match, got %d: %+v", len(entries), entries)
		}
		fields := entries[0].ContextMap()
		if fields["rate_limit_kind"] != "burst" {
			t.Errorf("expected the log entry to identify the burst variant, got fields: %+v", fields)
		}
	})

	t.Run("does not log when nothing matches", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		ctx := ctxzap.ToContext(context.Background(), zap.New(core))

		errTarget := &ErrorResponse{ErrorCode: "USER_LACKS_PERMISSIONS"}
		if got := reclassifyRateLimitError(ctx, errTarget, origErr); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
		if n := logs.Len(); n != 0 {
			t.Errorf("expected no log entries on a non-match, got %d", n)
		}
	})
}

// TestGetUsers_ClassifiesHourlyRateLimitAsRetryable is an end-to-end regression test,
// exercising the real request path (GetUsers -> doRequestCommon -> reclassifyRateLimitError)
// against a mock server that returns DocuSign's actual observed 400 body, rather than
// calling reclassifyRateLimitError directly.
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
