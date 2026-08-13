package connector

import (
	"context"
	"errors"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
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

	// Regression test: id == "" used to sail through to deriveFallback (e.g.
	// client.GroupHref(ctx, "")) whenever no sample href was available, producing a
	// malformed trailing-slash href — the exact failure mode clmHrefWithID's own
	// empty-newID check exists to catch on the sample path, but that check never ran
	// here since an empty id with no samples skips clmHrefWithID entirely.
	t.Run("rejects an empty id before trying either path", func(t *testing.T) {
		fallbackCalled = false
		if _, err := clmPreferredHref(ctx, "", nil, fallback); err == nil {
			t.Error("expected an error for an empty id, got nil")
		}
		if fallbackCalled {
			t.Error("expected the fallback NOT to be called for an empty id")
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

func TestClmSampleHrefsFrom(t *testing.T) {
	type entry struct{ Href string }
	hrefOf := func(e entry) string { return e.Href }

	t.Run("includes a non-empty profile href first", func(t *testing.T) {
		principal, err := rs.NewResource("g", clmGroupResourceType, "group-1", rs.WithResourceProfile(map[string]any{"href": "https://real.example.com/v2/acct-1/groups/group-1"}))
		if err != nil {
			t.Fatalf("NewResource: %v", err)
		}
		got := clmSampleHrefsFrom(principal, []entry{{Href: "https://other.example.com/v2/acct-1/groups/group-2"}}, hrefOf)
		want := []string{"https://real.example.com/v2/acct-1/groups/group-1", "https://other.example.com/v2/acct-1/groups/group-2"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("clmSampleHrefsFrom = %v, want %v", got, want)
		}
	})

	// Regression test: parseIntoClm*Resource always writes the "href" profile key, even
	// when the underlying CLM object has no Href yet, so GetProfileStringValue reports
	// ok == true for an empty string. Appending it anyway would make an
	// identity-only-of-real-samples principal look identical to one with a genuinely
	// malformed sample, tripping clmPreferredHref's unexpected-failure log on what is
	// actually the routine no-sample case.
	t.Run("excludes an empty profile href", func(t *testing.T) {
		principal, err := rs.NewResource("g", clmGroupResourceType, "group-1", rs.WithResourceProfile(map[string]any{"href": ""}))
		if err != nil {
			t.Fatalf("NewResource: %v", err)
		}
		got := clmSampleHrefsFrom(principal, []entry{{Href: "https://other.example.com/v2/acct-1/groups/group-2"}}, hrefOf)
		want := []string{"https://other.example.com/v2/acct-1/groups/group-2"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("clmSampleHrefsFrom = %v, want %v", got, want)
		}
	})

	// Same reasoning as the profile-href case above, but for an entry's Href — degenerate
	// data, not an unexpected shape, so it must be excluded the same way.
	t.Run("excludes an empty entry href", func(t *testing.T) {
		principal := &v2.Resource{Id: &v2.ResourceId{ResourceType: clmGroupResourceType.Id, Resource: "group-1"}}
		got := clmSampleHrefsFrom(principal, []entry{{Href: ""}, {Href: "https://other.example.com/v2/acct-1/groups/group-2"}}, hrefOf)
		want := []string{"https://other.example.com/v2/acct-1/groups/group-2"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("clmSampleHrefsFrom = %v, want %v", got, want)
		}
	})

	t.Run("no profile at all", func(t *testing.T) {
		principal := &v2.Resource{Id: &v2.ResourceId{ResourceType: clmGroupResourceType.Id, Resource: "group-1"}}
		got := clmSampleHrefsFrom(principal, []entry{{Href: "https://other.example.com/v2/acct-1/groups/group-2"}}, hrefOf)
		want := []string{"https://other.example.com/v2/acct-1/groups/group-2"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("clmSampleHrefsFrom = %v, want %v", got, want)
		}
	})
}
