package client

import (
	"encoding/json"
	"testing"
)

// TestClmErrorResponse_Message is a regression test: a live 401 from a real CLM tenant
// returned {"Error":{"UserMessage":"Access Denied","DeveloperMessage":"Access
// Denied","ErrorCode":103,...}} — the previous top-level "Message" field assumption
// never matched this shape, so every real CLM error surfaced as "unknown CLM API
// error" regardless of what CLM actually reported.
func TestClmErrorResponse_Message(t *testing.T) {
	t.Run("parses a real CLM error envelope", func(t *testing.T) {
		body := `{"Error":{"HttpStatusCode":401,"UserMessage":"Access Denied","DeveloperMessage":"Access Denied","ErrorCode":103,"ReferenceId":"4c2e455a-9d5e-42c8-8328-cf25bfec684f"}}`
		var e ClmErrorResponse
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if want := "CLM API error 103: Access Denied"; e.Message() != want {
			t.Errorf("Message() = %q, want %q", e.Message(), want)
		}
	})

	t.Run("includes DeveloperMessage when it differs from UserMessage", func(t *testing.T) {
		body := `{"Error":{"UserMessage":"Access Denied","DeveloperMessage":"token missing impersonation scope","ErrorCode":103}}`
		var e ClmErrorResponse
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if want := "CLM API error 103: Access Denied (token missing impersonation scope)"; e.Message() != want {
			t.Errorf("Message() = %q, want %q", e.Message(), want)
		}
	})

	t.Run("falls back to a generic message when the body is empty", func(t *testing.T) {
		var e ClmErrorResponse
		if want := "unknown CLM API error"; e.Message() != want {
			t.Errorf("Message() = %q, want %q", e.Message(), want)
		}
	})
}
