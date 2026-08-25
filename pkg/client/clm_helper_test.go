package client

import (
	"encoding/json"
	"net/url"
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
			got, err := getClmNextToken(tt.requested, tt.itemCount, tt.hasNext, tt.total, "")
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
	got, err := getClmNextToken(clmRequestedPage{Offset: 100, PageSize: 100}, 100, false, 200, "")
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
//
// The cap uses the larger of two independent estimates (see getClmNextToken's doc):
// requested.Requests, an actual per-request counter carried in the token, and
// nextOffset/PageSize, a floor recoverable from Offset alone with no history. Each
// sub-test below targets the scenario the other estimate alone would miss:
//   - "reaches the cap through normal advancement" / "does not fire just below the
//     cap": full pages, where both estimates agree — the ordinary case.
//   - "fires from a resumed request with no persisted page history": Requests is 0
//     (a pre-cap or round-tripped token), so only the offset floor catches it.
//   - "fires from a short-page runaway that the offset floor alone would miss": pages
//     far short of PageSize kept alive by hasNext, so nextOffset advances by far less
//     than PageSize per request — the offset floor alone would let this run up to
//     PageSize times longer than intended; only requested.Requests catches it promptly.
func TestGetClmNextToken_CapsRunawayPagination(t *testing.T) {
	t.Run("reaches the cap through normal advancement", func(t *testing.T) {
		requested := clmRequestedPage{Offset: (maxClmListPages - 1) * 100, PageSize: 100}
		_, err := getClmNextToken(requested, 100, false, 0, "")
		if err == nil {
			t.Fatal("expected an error once maxClmListPages is reached, got nil")
		}
	})

	t.Run("does not fire just below the cap", func(t *testing.T) {
		requested := clmRequestedPage{Offset: (maxClmListPages - 2) * 100, PageSize: 100}
		got, err := getClmNextToken(requested, 100, false, 0, "")
		if err != nil {
			t.Fatalf("expected no error just below the cap, got: %v", err)
		}
		if got == "" {
			t.Fatal("expected a continuation token just below the cap, got empty")
		}
	})

	t.Run("fires from a resumed request with no persisted page history", func(t *testing.T) {
		// Offset alone, with Requests at its zero value (as a pre-cap or
		// round-tripped token would decode to), must still trigger the cap.
		requested := clmRequestedPage{Offset: maxClmListPages * 100, PageSize: 100}
		_, err := getClmNextToken(requested, 100, false, 0, "")
		if err == nil {
			t.Fatal("expected the cap to fire from Offset alone, got nil error")
		}
	})

	t.Run("fires from a short-page runaway that the offset floor alone would miss", func(t *testing.T) {
		// itemCount (1) is far short of PageSize (100) but hasNext keeps forcing
		// continuation, so nextOffset only advances by 1 per request — the offset
		// floor (nextOffset/PageSize) would need PageSize times more requests than
		// requested.Requests to reach the same cap. Set Requests to exactly one below
		// the cap to isolate that it alone is what trips it here.
		requested := clmRequestedPage{Offset: maxClmListPages - 1, PageSize: 100, Requests: maxClmListPages - 1}
		_, err := getClmNextToken(requested, 1, true, 0, "")
		if err == nil {
			t.Fatal("expected the request-count estimate to trip the cap even though the offset floor would not have")
		}
	})
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

// TestValidateClmURL is a regression test for a review finding: an exact-host check
// against the discovered CLM base URL would hard-fail every Task API call the first
// time CLM legitimately serves a task/result href from a sibling host — this package's
// own confirmed base-URL-resolution flow already spans two unrelated domains
// (*.clm.docusign.net for the Object API, auth.springcm.com for discovery), so
// validateClmURL checks domain family instead of exact host equality.
func TestValidateClmURL(t *testing.T) {
	c := &Client{clmBaseURI: "https://api.na1.clm.docusign.net"}

	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{"exact host match", "https://api.na1.clm.docusign.net/v2/acct/foldersearchtasks/1", false},
		{"sibling docusign.net host", "https://tasks.na2.clm.docusign.net/v2/acct/foldersearchtasks/1", false},
		{"springcm.com sibling (CLM's other confirmed domain)", "https://auth.springcm.com/v2/acct/foldersearchtasks/1", false},
		{"unrelated host", "https://attacker.example.com/v2/acct/foldersearchtasks/1", true},
		{"docusign.net as a suffix of an unrelated domain is not a match", "https://evil-docusign.net.attacker.com/x", true},
		{"scheme mismatch", "http://api.na1.clm.docusign.net/v2/acct/foldersearchtasks/1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("url.Parse: %v", err)
			}
			err = c.validateClmURL(u, "test href")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateClmURL(%q) error = %v, wantErr %v", tt.rawURL, err, tt.wantErr)
			}
		})
	}

	// A --base-url/mock target has no domain of its own to fall back on, so the exact
	// match must compare host+port, not just host: switching to Hostname() (which
	// strips the port) for that branch would let a same-host, different-port href
	// through.
	t.Run("exact-match branch rejects a same-host different-port href", func(t *testing.T) {
		mockClient := &Client{clmBaseURI: "http://127.0.0.1:5000"}
		u, err := url.Parse("http://127.0.0.1:9999/v2/acct/foldersearchtasks/1")
		if err != nil {
			t.Fatalf("url.Parse: %v", err)
		}
		if err := mockClient.validateClmURL(u, "test href"); err == nil {
			t.Fatal("expected a different port on a non-domain host to be rejected")
		}
	})
}
