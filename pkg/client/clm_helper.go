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
// rather than from the response.
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

// getClmNextToken calculates the next-page token from a CLM collection response, using
// two independent signals rather than trusting response fields that were never
// confirmed to be populated reliably:
//
//   - A short page (fewer items than requested) means this is the last page,
//     regardless of what Total says. This guards against Total being zero or absent
//     causing premature termination that would silently drop every resource beyond
//     page one.
//   - Total, when it's actually nonzero, gives an earlier exact stop than waiting for
//     a short page — but is only trusted to stop early, never to justify continuing
//     past a full page.
//
// Neither the response's Offset nor Total needs to be accurate for pagination to
// terminate correctly: a short page always stops it. Worst case, an exact
// total/page-size boundary costs one extra request past the true last page, which
// itself comes back empty and stops via the itemCount == 0 check — not an infinite
// loop, unlike trusting a possibly-unreliable Offset would risk (see requested's doc).
func getClmNextToken(requested clmRequestedPage, itemCount, total int) string {
	if itemCount == 0 || itemCount < requested.PageSize {
		return ""
	}
	nextOffset := requested.Offset + itemCount
	if total > 0 && nextOffset >= total {
		return ""
	}
	return encodeClmPageToken(&clmPageToken{Offset: nextOffset})
}
