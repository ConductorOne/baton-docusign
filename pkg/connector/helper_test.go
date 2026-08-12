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
		name     string
		profiles []client.PermissionProfile
		lookup   string
		wantID   string
		wantOK   bool
	}{
		{
			name: "single match",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
			},
			lookup: "DocuSign Admin",
			wantID: "pp-1",
			wantOK: true,
		},
		{
			name: "no match",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "pp-1", PermissionProfileName: "DocuSign Admin"},
			},
			lookup: "Nonexistent",
			wantOK: false,
		},
		{
			name: "match with no usable ID counts as not found",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "", PermissionProfileName: "DocuSign Admin"},
			},
			lookup: "DocuSign Admin",
			wantOK: false,
		},
		{
			// DocuSign does not guarantee PermissionProfileName is unique per account —
			// picking the first match here would risk granting/revoking the wrong
			// profile, so an ambiguous name must be treated as not found.
			name: "ambiguous name is treated as not found, not the first match",
			profiles: []client.PermissionProfile{
				{PermissionProfileId: "pp-1", PermissionProfileName: "Custom Profile"},
				{PermissionProfileId: "pp-2", PermissionProfileName: "Custom Profile"},
			},
			lookup: "Custom Profile",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := permissionProfileIDByName(tt.profiles, tt.lookup)
			if gotOK != tt.wantOK || (gotOK && gotID != tt.wantID) {
				t.Errorf("permissionProfileIDByName(%v, %q) = (%q, %v), want (%q, %v)", tt.profiles, tt.lookup, gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}
