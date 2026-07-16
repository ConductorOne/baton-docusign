package client

import (
	"encoding/json"
	"testing"
)

// TestGetClmNextToken_ComputesFromRequestNotResponse is a regression test covering
// failure modes raised in review, none of which were confirmed against a live CLM
// account:
//
//  1. getClmNextToken used to derive the next offset from the response's own Offset
//     field. If the API doesn't echo that back accurately, the token never advances,
//     looping the SDK's own pagination driver indefinitely (none of SearchFolders,
//     ListGroups, ListMembers, ListPermissionSets, GetGroupMembers have their own
//     non-advancing-token guard the way GetMemberGroups does internally).
//  2. Trusting the response's Total field to decide when to stop risks the opposite
//     failure: if Total is zero or absent, terminating as soon as
//     requestOffset+itemCount >= total (i.e. immediately, since total is 0) would
//     silently drop every resource beyond page one.
//  3. An earlier fix treated a short page (fewer items than requested) as always the
//     last page. That's also risky: if the CLM API ever caps its effective page size
//     below what was requested while more data remains server-side, a short page
//     wouldn't mean "done" — it would silently truncate the sync just the same. So a
//     short page only stops pagination when Total also confirms nothing is left;
//     otherwise it keeps going.
//
// The fix removes the response's Offset from the signature entirely (requestOffset is
// always what the caller itself asked for) and only trusts Total to stop *early*, never
// to justify stopping on a short/full page alone.
func TestGetClmNextToken_ComputesFromRequestNotResponse(t *testing.T) {
	tests := []struct {
		name          string
		requestOffset int
		itemCount     int
		total         int
		wantEmpty     bool
		wantNextOfft  int
	}{
		{"advances on a full page", 0, 100, 250, false, 100},
		{"advances from a non-zero offset", 100, 100, 250, false, 200},
		{"terminates on a short last page confirmed by total", 200, 50, 250, true, 0},
		{"terminates when a full page reaches total exactly", 200, 100, 250, true, 0},
		{"terminates on an empty page even if total says more remain", 100, 0, 250, true, 0},
		{"does not terminate early on a full page when total is zero/unreliable", 0, 100, 0, false, 100},
		{"does NOT terminate on a short page when total is zero/unreliable — total can't confirm it's actually the last page", 200, 50, 0, false, 250},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getClmNextToken(tt.requestOffset, tt.itemCount, tt.total)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("expected an empty (terminating) token, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected a non-empty continuation token, got empty")
			}
			decoded, err := decodeClmPageToken(got)
			if err != nil {
				t.Fatalf("decodeClmPageToken: %v", err)
			}
			if decoded.Offset != tt.wantNextOfft {
				t.Errorf("expected next offset %d, got %d", tt.wantNextOfft, decoded.Offset)
			}
		})
	}
}

// TestGetClmNextToken_ExactBoundaryDoesNotLoop documents that when Total is an exact
// multiple of the page size, a full page landing exactly on Total stops immediately —
// no need to wait for an empty page in this case, since Total confirms it.
func TestGetClmNextToken_ExactBoundaryDoesNotLoop(t *testing.T) {
	got := getClmNextToken(100, 100, 200)
	if got != "" {
		t.Fatalf("expected termination when a full page lands exactly on total, got %q", got)
	}
}

// TestGetClmNextToken_UnreliableTotalEventuallyTerminates documents that when Total
// can't be trusted (zero/unpopulated) and the API caps pages below what was requested,
// pagination still terminates — just via the empty final page rather than the short
// page itself — so it costs a few extra requests, never an infinite loop.
func TestGetClmNextToken_UnreliableTotalEventuallyTerminates(t *testing.T) {
	// A short page (30 items) with no reliable Total must NOT stop here...
	next := getClmNextToken(0, 30, 0)
	if next == "" {
		t.Fatal("expected pagination to continue past a short page when total can't confirm it's the last one")
	}
	decoded, err := decodeClmPageToken(next)
	if err != nil {
		t.Fatalf("decodeClmPageToken: %v", err)
	}
	if decoded.Offset != 30 {
		t.Fatalf("expected next offset 30, got %d", decoded.Offset)
	}
	// ...but the walk still ends once the API actually runs out of items.
	if got := getClmNextToken(decoded.Offset, 0, 0); got != "" {
		t.Fatalf("expected termination on the eventual empty page, got %q", got)
	}
}

// rawJSONFields builds a map[string]json.RawMessage from plain Go values, for
// constructing hand-built CLM account discovery responses in tests below.
func rawJSONFields(t *testing.T, fields map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%q): %v", k, err)
		}
		out[k] = b
	}
	return out
}

// TestClmExtractBaseURLField_FindsRecognizedFieldRegardlessOfShape is a regression
// test for the api_base_url resolution finding raised in review: the CLM account
// discovery endpoint's exact response schema was not available when this was written,
// so ensureClmInitialized must recognize the base URL under any of several plausible
// CLM-specific field names rather than assuming one specific shape, and must fail
// clearly rather than silently resolving to an empty string when none of them are
// present. Generic names like "BaseUrl"/"base_url" are deliberately not candidates —
// see clmBaseURLCandidateFields's doc for why matching one could silently misroute
// CLM traffic instead of failing loudly.
func TestClmExtractBaseURLField_FindsRecognizedFieldRegardlessOfShape(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		wantURL string
		wantOK  bool
	}{
		{
			"ApiBaseUrl (highest priority, matches CLM's legacy token response field)",
			map[string]any{ClmDiscoveryFieldAPIBaseURL: "https://api.na1.clm.docusign.net"},
			"https://api.na1.clm.docusign.net", true,
		},
		{"snake_case api_base_url", map[string]any{"api_base_url": "https://api.eu1.clm.docusign.net"}, "https://api.eu1.clm.docusign.net", true},
		{"ObjectApiUrl fallback", map[string]any{"ObjectApiUrl": "https://api.na2.clm.docusign.net"}, "https://api.na2.clm.docusign.net", true},
		{
			"prefers the first match when multiple candidates are present",
			map[string]any{ClmDiscoveryFieldAPIBaseURL: "https://first.example", "object_api_url": "https://second.example"},
			"https://first.example", true,
		},
		{"empty string value does not count as a match", map[string]any{ClmDiscoveryFieldAPIBaseURL: ""}, "", false},
		{"no recognized field at all", map[string]any{"SomeOtherField": "unrelated", "AccountId": "acct-123"}, "", false},
		{
			"generic BaseUrl/base_url are not recognized, to avoid matching an unrelated field",
			map[string]any{"BaseUrl": "https://maybe-unrelated.example", "base_url": "https://also-maybe-unrelated.example"},
			"", false,
		},
		{"empty response body", map[string]any{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clmExtractBaseURLField(rawJSONFields(t, tt.raw))
			if ok != tt.wantOK || got != tt.wantURL {
				t.Errorf("clmExtractBaseURLField(%+v) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.wantURL, tt.wantOK)
			}
		})
	}
}
