package connector

import (
	"errors"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
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

func TestPermissionProfileIDByName(t *testing.T) {
	tests := []struct {
		name        string
		profiles    []client.PermissionProfile
		lookup      string
		wantID      string
		wantMatches int
	}{
		{
			name: "single match",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
			},
			lookup:      "DocuSign Admin",
			wantID:      "pp-1",
			wantMatches: 1,
		},
		{
			name: "no match",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
			},
			lookup:      "Nonexistent",
			wantMatches: 0,
		},
		{
			name: "match with no usable ID doesn't count",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "", PermissionProfileName: "DocuSign Admin"},
			},
			lookup:      "DocuSign Admin",
			wantMatches: 0,
		},
		{
			name: "ambiguous name reports the match count, not the first match",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "pp-1", PermissionProfileName: "Custom Profile"},
				{PermissionProfileId: "pp-2", PermissionProfileName: "Custom Profile"},
			},
			lookup:      "Custom Profile",
			wantMatches: 2,
		},
		{
			// A same-name profile with no usable ID doesn't count toward "ambiguous" —
			// only the one profile with a real ID matters here.
			name: "same name, one with no usable ID, resolves to the valid one",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "", PermissionProfileName: "Custom Profile"},
				{PermissionProfileId: "pp-2", PermissionProfileName: "Custom Profile"},
			},
			lookup:      "Custom Profile",
			wantID:      "pp-2",
			wantMatches: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotMatches := permissionProfileIDByName(tt.profiles, tt.lookup)
			if gotMatches != tt.wantMatches || (gotMatches == 1 && gotID != tt.wantID) {
				t.Errorf("permissionProfileIDByName(%v, %q) = (%q, %d), want (%q, %d)", tt.profiles, tt.lookup, gotID, gotMatches, tt.wantID, tt.wantMatches)
			}
		})
	}
}
