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
