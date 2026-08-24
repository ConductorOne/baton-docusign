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

	t.Run("uses DeveloperMessage as the primary text when UserMessage is empty", func(t *testing.T) {
		body := `{"Error":{"DeveloperMessage":"token missing impersonation scope","ErrorCode":103}}`
		var e ClmErrorResponse
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if want := "CLM API error 103: token missing impersonation scope"; e.Message() != want {
			t.Errorf("Message() = %q, want %q", e.Message(), want)
		}
	})
}

// TestClmUserSecurityEntry_UnmarshalJSON is a regression test for a review finding:
// ClmUserSecurityEntry's wire shape is unconfirmed live (unlike Groups), and the
// original UnmarshalJSON only handled the nested {Item:{...}} shape — a flat
// {Href,...} response would have decoded with Href == "" silently (a missing "Item"
// key leaves a struct-typed field at its zero value, not a decode error), and since
// clmFolderSecurityToWrite round-trips the complete security state on every
// Grant/Revoke, that would blank and then drop every other user's folder access on an
// unrelated write.
func TestClmUserSecurityEntry_UnmarshalJSON(t *testing.T) {
	t.Run("nested Item shape (Groups' confirmed shape)", func(t *testing.T) {
		body := `{"Item":{"Href":"https://clm.example.com/v2/acct/members/1","Email":"a@example.com"},"AccessType":"View"}`
		var e ClmUserSecurityEntry
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if e.Href != "https://clm.example.com/v2/acct/members/1" || e.AccessType != "View" {
			t.Errorf("got %+v", e)
		}
	})

	t.Run("flat shape falls back instead of leaving Href empty", func(t *testing.T) {
		body := `{"Href":"https://clm.example.com/v2/acct/members/1","Email":"a@example.com","AccessType":"View"}`
		var e ClmUserSecurityEntry
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if e.Href != "https://clm.example.com/v2/acct/members/1" {
			t.Errorf("expected the flat shape's Href to be picked up, got %+v", e)
		}
		if e.AccessType != "View" {
			t.Errorf("got %+v", e)
		}
	})

	for _, tt := range []struct {
		name string
		body string
	}{
		{"Item is null", `{"Item":null,"AccessType":"View"}`},
		{"Item is an empty object", `{"Item":{},"AccessType":"View"}`},
		{"member nested under an unrecognized key", `{"Member":{"Href":"https://clm.example.com/v2/acct/members/1"},"AccessType":"View"}`},
	} {
		t.Run("fails loud instead of silently blanking Href: "+tt.name, func(t *testing.T) {
			var e ClmUserSecurityEntry
			if err := json.Unmarshal([]byte(tt.body), &e); err == nil {
				t.Fatalf("expected an error for an unrecognized wire shape, got %+v", e)
			}
		})
	}
}

// TestClmGroupSecurityEntry_UnmarshalJSON_FailsLoudOnUnrecognizedShape mirrors
// ClmUserSecurityEntry's identical fail-loud guard: Groups' nested shape is confirmed
// live today, but if that ever regresses, an empty-Href entry silently round-tripped
// into a PatchFolderSecurity body would drop that group's real folder access under
// replace semantics.
func TestClmGroupSecurityEntry_UnmarshalJSON_FailsLoudOnUnrecognizedShape(t *testing.T) {
	var e ClmGroupSecurityEntry
	if err := json.Unmarshal([]byte(`{"Item":{},"AccessType":"View"}`), &e); err == nil {
		t.Fatalf("expected an error for an unrecognized wire shape, got %+v", e)
	}
}

// TestClmRoleSecurityEntry_UnmarshalJSON is a regression test for a review finding:
// Item was a bare Go string, confirmed live only as a flat string — if CLM ever nests
// Roles the way Groups turned out to be nested, json.Unmarshal would hard-error
// decoding a JSON object into a string field, failing every clm_folder read/Grant/
// Revoke on that folder instead of just the one entry.
func TestClmRoleSecurityEntry_UnmarshalJSON(t *testing.T) {
	t.Run("flat string shape (confirmed live)", func(t *testing.T) {
		body := `{"AccessType":"View","Item":"FullSubscriber"}`
		var e ClmRoleSecurityEntry
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if e.Item != "FullSubscriber" || e.AccessType != "View" {
			t.Errorf("got %+v", e)
		}
	})

	t.Run("nested object shape doesn't hard-fail", func(t *testing.T) {
		body := `{"AccessType":"View","Item":{"Name":"FullSubscriber","Href":"https://clm.example.com/v2/acct/roles/1"}}`
		var e ClmRoleSecurityEntry
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if e.Item != "FullSubscriber" || e.AccessType != "View" {
			t.Errorf("got %+v", e)
		}
	})
}
