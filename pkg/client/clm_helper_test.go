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
//     looping the SDK's own pagination driver indefinitely. Deriving nextOffset from
//     the request instead of the response fixes that specific case; maxClmListPages
//     (see TestGetClmNextToken_CapsRunawayPagination) is the backstop for the more
//     general case where the API ignores the offset entirely.
//  2. Trusting the response's Total field to decide when to stop risks the opposite
//     failure: if Total is zero or absent, terminating as soon as
//     requestOffset+itemCount >= total (i.e. immediately, since total is 0) would
//     silently drop every resource beyond page one.
//  3. A short page (fewer items than requested) normally means "last page" — the
//     common case for REST APIs that honor the requested limit — so it stops
//     pagination unless hasNext (the response's own Next field, non-empty) explicitly
//     says otherwise. An earlier version instead kept paginating on any short page
//     whenever Total was unreliable, which traded that hypothetical risk for a more
//     concrete one: the extra request it issued past a short page targets an
//     out-of-range offset, and if the API rejects that with a 4xx instead of an empty
//     200, the whole sync would fail on what was otherwise a complete, successful
//     result — flagged in review by two reviewers independently.
func TestGetClmNextToken_ComputesFromRequestNotResponse(t *testing.T) {
	tests := []struct {
		name         string
		requested    clmRequestedPage
		itemCount    int
		hasNext      bool
		total        int
		wantEmpty    bool
		wantNextOfft int
	}{
		{"advances on a full page", clmRequestedPage{Offset: 0, PageSize: 100}, 100, false, 250, false, 100},
		{"advances from a non-zero offset", clmRequestedPage{Offset: 100, PageSize: 100}, 100, false, 250, false, 200},
		{"terminates on a short last page confirmed by total", clmRequestedPage{Offset: 200, PageSize: 100}, 50, false, 250, true, 0},
		{"terminates when a full page reaches total exactly", clmRequestedPage{Offset: 200, PageSize: 100}, 100, false, 250, true, 0},
		{"terminates on an empty page even if total/hasNext say more remain", clmRequestedPage{Offset: 100, PageSize: 100}, 0, true, 250, true, 0},
		{"does not terminate early on a full page when total is zero/unreliable", clmRequestedPage{Offset: 0, PageSize: 100}, 100, false, 0, false, 100},
		{"terminates on a short page when neither total nor hasNext say otherwise (the common case)", clmRequestedPage{Offset: 200, PageSize: 100}, 50, false, 0, true, 0},
		{"does NOT terminate on a short page when hasNext explicitly says there's more", clmRequestedPage{Offset: 200, PageSize: 100}, 50, true, 0, false, 250},
		{"does NOT terminate on a short page when total explicitly says more remains", clmRequestedPage{Offset: 0, PageSize: 100}, 60, false, 250, false, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getClmNextToken(tt.requested, tt.itemCount, tt.hasNext, tt.total)
			if err != nil {
				t.Fatalf("getClmNextToken: %v", err)
			}
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
	got, err := getClmNextToken(clmRequestedPage{Offset: 100, PageSize: 100}, 100, false, 200)
	if err != nil {
		t.Fatalf("getClmNextToken: %v", err)
	}
	if got != "" {
		t.Fatalf("expected termination when a full page lands exactly on total, got %q", got)
	}
}

// TestGetClmNextToken_CapsRunawayPagination is a regression test for a scenario raised
// in review: if a CLM list endpoint ignores pageSortParams.offset (always returning a
// full page) and Total is unpopulated, nextOffset advances in this function's own
// accounting forever even though the server-side data never changes — unlike
// GetMemberGroups, these SDK-driven paths have no way to detect the response itself
// isn't advancing (the SDK drives one page per call, with no cross-call memory beyond
// the token), so maxClmListPages is the only local safeguard. This confirms the cap
// actually fires — a real regression here would otherwise page forever, not just
// return a wrong answer, so it's worth a dedicated test even though the earlier
// table test already covers the "does not terminate" branches this reaches through.
func TestGetClmNextToken_CapsRunawayPagination(t *testing.T) {
	requested := clmRequestedPage{Offset: 0, PageSize: 100, Page: maxClmListPages - 1}
	_, err := getClmNextToken(requested, 100, false, 0)
	if err == nil {
		t.Fatal("expected an error once maxClmListPages is reached, got nil")
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
