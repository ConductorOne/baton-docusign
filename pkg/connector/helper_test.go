package connector

import (
	"context"
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

	// A bare scheme+host contains a "/" too (separating scheme from host), so this must
	// be rejected on its own — not accepted as a valid sample producing a garbage
	// "https://x"-shaped href with no real path.
	if _, err := clmHrefWithID("https://clm.example.com", "x"); err == nil {
		t.Error("expected an error for a sample href with no path segment (bare scheme+host)")
	}

	// An empty newID would otherwise pass every shape check on sampleHref and silently
	// return a trailing-slash href with no ID segment at all (e.g.
	// ".../groups/") — a malformed href with nothing pointing back at "the ID was empty".
	if _, err := clmHrefWithID("https://clm.example.com/v2/acct-1/groups/group-old", ""); err == nil {
		t.Error("expected an error for an empty newID")
	}
}

func TestClmPreferredHref(t *testing.T) {
	ctx := context.Background()
	fallbackCalled := false
	fallback := func() (string, error) {
		fallbackCalled = true
		return "https://derived.example.com/v2/acct-1/groups/group-target", nil
	}

	t.Run("prefers a real sample href over the fallback", func(t *testing.T) {
		fallbackCalled = false
		got, err := clmPreferredHref(ctx, "group-target", []string{"https://real.example.com/v2/acct-1/groups/group-other"}, fallback)
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
		got, err := clmPreferredHref(ctx, "group-target", []string{"", "https://real.example.com/v2/acct-1/groups/group-other"}, fallback)
		if err != nil {
			t.Fatalf("clmPreferredHref: %v", err)
		}
		if want := "https://real.example.com/v2/acct-1/groups/group-target"; got != want {
			t.Errorf("clmPreferredHref = %q, want %q", got, want)
		}
	})

	t.Run("falls back when no sample href is available", func(t *testing.T) {
		fallbackCalled = false
		got, err := clmPreferredHref(ctx, "group-target", nil, fallback)
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

	// Regression test: sampleHrefs is non-empty but every sample is malformed in an
	// unexpected way (not the routine "no sample yet" case) — must still fall back
	// safely rather than erroring, even though this case is now logged (see
	// clmPreferredHref's doc).
	t.Run("falls back when every sample href fails to parse", func(t *testing.T) {
		fallbackCalled = false
		got, err := clmPreferredHref(ctx, "group-target", []string{"no-path-separator", "https://clm.example.com"}, fallback)
		if err != nil {
			t.Fatalf("clmPreferredHref: %v", err)
		}
		if want := "https://derived.example.com/v2/acct-1/groups/group-target"; got != want {
			t.Errorf("clmPreferredHref = %q, want %q", got, want)
		}
		if !fallbackCalled {
			t.Error("expected the fallback to be called when every sample href fails to parse")
		}
	})
}
