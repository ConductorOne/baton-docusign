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
// case for REST APIs that honor the requested limit — but treats hasNext (the
// response's own Next field, non-empty) as an explicit override: if the API says there's
// more, keep going even on a short page, without needing to guess or issue a probing
// request that might fail. Only three signals are trusted, none of which requires an
// extra request past the true end: itemCount == 0 always stops; Total, when nonzero,
// stops pagination early once requestOffset+itemCount reaches or passes it; hasNext
// keeps it going past what would otherwise look like a final short page.
func getClmNextToken(requested clmRequestedPage, itemCount int, hasNext bool, total int) string {
	if itemCount == 0 {
		return ""
	}
	nextOffset := requested.Offset + itemCount
	if total > 0 && nextOffset >= total {
		return ""
	}
	if itemCount < requested.PageSize && !hasNext {
		return ""
	}
	return encodeClmPageToken(&clmPageToken{Offset: nextOffset})
}
