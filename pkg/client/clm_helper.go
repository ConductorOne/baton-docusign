package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

// clmRequestedPage records what a paged CLM request actually asked for — offset and
// page size — so the response can be paginated against ground truth instead of trusting
// response fields that were never confirmed to be populated reliably (see
// getClmNextToken).
type clmRequestedPage struct {
	Offset   int
	PageSize int
}

// preparePagedRequestClm prepares a paged request URL using CLM's confirmed query
// param names (pageSortParams.offset/limit/...) — distinct from eSignature's
// start_position/count (see preparePagedRequest in helper.go). Returns what it
// requested alongside the URL, so callers can compute the next-page token from that
// rather than from the response (see getClmNextToken).
func preparePagedRequestClm(baseURL *url.URL, endpoint string, options PageOptions) (*url.URL, clmRequestedPage, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, clmRequestedPage{}, fmt.Errorf("baton-docusign: invalid CLM endpoint: %w", err)
	}

	fullURL := baseURL.ResolveReference(endpointURL)
	q := fullURL.Query()

	offset := 0
	if options.PageToken != "" {
		decoded, err := decodeClmPageToken(options.PageToken)
		if err != nil {
			return nil, clmRequestedPage{}, fmt.Errorf("baton-docusign: invalid CLM page token: %w", err)
		}
		offset = decoded.Offset
	}
	q.Set("pageSortParams.offset", fmt.Sprintf("%d", offset))

	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	q.Set("pageSortParams.limit", fmt.Sprintf("%d", pageSize))

	fullURL.RawQuery = q.Encode()
	return fullURL, clmRequestedPage{Offset: offset, PageSize: pageSize}, nil
}

// clmPageToken is the internal offset-based continuation token for CLM pagination.
type clmPageToken struct {
	Offset int `json:"offset"`
}

func encodeClmPageToken(pt *clmPageToken) string {
	b, _ := json.Marshal(pt)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeClmPageToken(token string) (*clmPageToken, error) {
	if token == "" {
		return &clmPageToken{Offset: 0}, nil
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	var pt clmPageToken
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, err
	}
	return &pt, nil
}

// getClmNextToken calculates the next-page token from a CLM collection response.
// requestOffset/itemCount/total were already being second-guessed for reliability; an
// earlier version of this function also made a short page (fewer items than requested)
// keep paginating unless Total confirmed the end, to guard against the API capping its
// effective page size below what was requested. That traded one unconfirmed risk for a
// more concrete one: the extra request it issued past a short page targets an
// out-of-range offset, and if any CLM list/search endpoint rejects that with a 4xx
// instead of an empty 200, the whole sync would fail on what was otherwise its last,
// complete page — a regression flagged in review by two independent reviewers.
//
// This version stops on a short page like the original, simple behavior — the common
// case for REST APIs that honor the requested limit — but two explicit signals can
// override that and force it to keep going: hasNext (the response's own Next field,
// non-empty), or Total itself, when it's nonzero and higher than what's been collected
// so far. Without that second override, a short page with Total populated and clearly
// indicating more remains (e.g. 60 items back on a 100-item page request, with Total
// 250) would incorrectly stop at 60 — Total was only being used to justify stopping
// early, never to override the short-page heuristic that fires independently of it, so
// a positive Total signal was getting silently ignored. Total is still never used to
// justify continuing past what it says is the actual end (the first check below), and
// no signal here requires an extra request past the true end.
func getClmNextToken(requested clmRequestedPage, itemCount int, hasNext bool, total int) string {
	if itemCount == 0 {
		return ""
	}
	nextOffset := requested.Offset + itemCount
	if total > 0 && nextOffset >= total {
		return ""
	}
	totalSaysMoreRemains := total > 0 && nextOffset < total
	if itemCount < requested.PageSize && !hasNext && !totalSaysMoreRemains {
		return ""
	}
	return encodeClmPageToken(&clmPageToken{Offset: nextOffset})
}
