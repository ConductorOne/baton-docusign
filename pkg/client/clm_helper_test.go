package client

import (
	"encoding/json"
	"testing"
)

// TestGetClmNextToken_ComputesFromRequestNotResponse is a regression test covering two
// independent failure modes raised in review, neither of which was confirmed against a
// live CLM account:
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
//
// The fix removes the response's Offset from the signature entirely (requestedPage is
// always what the caller itself asked for) and only trusts Total to stop *early*, never
// to justify stopping when a full page was actually returned — the real termination
// signal is a short page (fewer items than requested), which is self-consistent
// regardless of what Offset/Total the API echoes back.
func TestGetClmNextToken_ComputesFromRequestNotResponse(t *testing.T) {
	tests := []struct {
		name         string
		requested    clmRequestedPage
		itemCount    int
		total        int
		wantEmpty    bool
		wantNextOfft int
	}{
		{"advances on a full page", clmRequestedPage{Offset: 0, PageSize: 100}, 100, 250, false, 100},
		{"advances from a non-zero offset", clmRequestedPage{Offset: 100, PageSize: 100}, 100, 250, false, 200},
		{"terminates on a short last page", clmRequestedPage{Offset: 200, PageSize: 100}, 50, 250, true, 0},
		{"terminates when a full page reaches total exactly", clmRequestedPage{Offset: 200, PageSize: 100}, 100, 250, true, 0},
		{"terminates on an empty page even if total says more remain", clmRequestedPage{Offset: 100, PageSize: 100}, 0, 250, true, 0},
		{"does not terminate early on a full page when total is zero/unreliable", clmRequestedPage{Offset: 0, PageSize: 100}, 100, 0, false, 100},
		{"still terminates on a short page when total is zero/unreliable", clmRequestedPage{Offset: 200, PageSize: 100}, 50, 0, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getClmNextToken(tt.requested, tt.itemCount, tt.total)
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
// multiple of the page size, the one extra request past the true last page terminates
// via the empty-page check rather than looping — a full last page (itemCount ==
// PageSize) can't be distinguished from "there might be more" by the short-page rule
// alone, so this case costs one harmless extra round trip, not an infinite loop.
func TestGetClmNextToken_ExactBoundaryDoesNotLoop(t *testing.T) {
	// Total is exactly 2 full pages of 100; page 2 (offset=100) is a full page landing
	// exactly on total, so the total>0 check catches it and stops immediately.
	got := getClmNextToken(clmRequestedPage{Offset: 100, PageSize: 100}, 100, 200)
	if got != "" {
		t.Fatalf("expected termination when a full page lands exactly on total, got %q", got)
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
// field names rather than assuming one specific shape, and must fail clearly rather
// than silently resolving to an empty string when none of them are present.
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
			map[string]any{ClmDiscoveryFieldAPIBaseURL: "https://first.example", "BaseUrl": "https://second.example"},
			"https://first.example", true,
		},
		{"empty string value does not count as a match", map[string]any{ClmDiscoveryFieldAPIBaseURL: ""}, "", false},
		{"no recognized field at all", map[string]any{"SomeOtherField": "unrelated", "AccountId": "acct-123"}, "", false},
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
