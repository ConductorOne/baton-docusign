package connector

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsOptInFeatureUnavailableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"permission denied", status.Error(codes.PermissionDenied, "no CLM subscription"), true},
		{"unauthenticated", status.Error(codes.Unauthenticated, "insufficient scope"), true},
		{"not found (e.g. account never provisioned in SpringCM)", status.Error(codes.NotFound, "no such account"), true},
		{"failed precondition (e.g. discovery response missing a recognized base-URL field)", status.Error(codes.FailedPrecondition, "no recognized field"), true},
		{"unavailable (rate limit/5xx)", status.Error(codes.Unavailable, "rate limited"), false},
		{"internal", status.Error(codes.Internal, "boom"), false},
		{"unknown (bare unwrapped error)", status.Error(codes.Unknown, "boom"), false},
		{"plain non-gRPC error", errors.New("some transport error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOptInFeatureUnavailableError(tt.err); got != tt.want {
				t.Errorf("isOptInFeatureUnavailableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestClmHrefWithID(t *testing.T) {
	got, err := clmHrefWithID("https://clm.example.com/v2/acct-1/groups/group-old", "group-new")
	if err != nil {
		t.Fatalf("clmHrefWithID: %v", err)
	}
	if want := "https://clm.example.com/v2/acct-1/groups/group-new"; got != want {
		t.Errorf("clmHrefWithID = %q, want %q", got, want)
	}

	if _, err := clmHrefWithID("no-path-separator", "x"); err == nil {
		t.Error("expected an error for a sample href with no path separator")
	}
}

func TestClmPreferredHref(t *testing.T) {
	fallbackCalled := false
	fallback := func() (string, error) {
		fallbackCalled = true
		return "https://derived.example.com/v2/acct-1/groups/group-target", nil
	}

	t.Run("prefers a real sample href over the fallback", func(t *testing.T) {
		fallbackCalled = false
		got, err := clmPreferredHref("group-target", []string{"https://real.example.com/v2/acct-1/groups/group-other"}, fallback)
		if err != nil {
			t.Fatalf("clmPreferredHref: %v", err)
		}
		if want := "https://real.example.com/v2/acct-1/groups/group-target"; got != want {
			t.Errorf("clmPreferredHref = %q, want %q", got, want)
		}
		if fallbackCalled {
			t.Error("expected the fallback NOT to be called when a real sample href is available")
		}
	})

	t.Run("skips empty sample hrefs", func(t *testing.T) {
		fallbackCalled = false
		got, err := clmPreferredHref("group-target", []string{"", "https://real.example.com/v2/acct-1/groups/group-other"}, fallback)
		if err != nil {
			t.Fatalf("clmPreferredHref: %v", err)
		}
		if want := "https://real.example.com/v2/acct-1/groups/group-target"; got != want {
			t.Errorf("clmPreferredHref = %q, want %q", got, want)
		}
	})

	t.Run("falls back when no sample href is available", func(t *testing.T) {
		fallbackCalled = false
		got, err := clmPreferredHref("group-target", nil, fallback)
		if err != nil {
			t.Fatalf("clmPreferredHref: %v", err)
		}
		if want := "https://derived.example.com/v2/acct-1/groups/group-target"; got != want {
			t.Errorf("clmPreferredHref = %q, want %q", got, want)
		}
		if !fallbackCalled {
			t.Error("expected the fallback to be called when no sample hrefs are available")
		}
	})
}
