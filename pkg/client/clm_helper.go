package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

// preparePagedRequestClm prepares a paged request URL using CLM's confirmed query
// param names (pageSortParams.offset/limit/...) — distinct from eSignature's
// start_position/count (see preparePagedRequest in helper.go). Returns the offset it
// requested alongside the URL, so callers can compute the next-page token from that
// rather than from the response (see getClmNextToken).
func preparePagedRequestClm(baseURL *url.URL, endpoint string, options PageOptions) (*url.URL, int, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, fmt.Errorf("baton-docusign: invalid CLM endpoint: %w", err)
	}

	fullURL := baseURL.ResolveReference(endpointURL)
	q := fullURL.Query()

	offset := 0
	if options.PageToken != "" {
		decoded, err := decodeClmPageToken(options.PageToken)
		if err != nil {
			return nil, 0, fmt.Errorf("baton-docusign: invalid CLM page token: %w", err)
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
	return fullURL, offset, nil
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
// two signals instead of trusting response fields that were never confirmed to be
// populated reliably:
//
//   - An empty page (zero items) always means stop.
//   - Total, when it's actually nonzero, stops pagination once requestOffset+itemCount
//     reaches or passes it.
//
// A short page (fewer items than requested) is deliberately NOT treated as "this must
// be the last page": if the CLM API ever caps its effective page size below what was
// requested while more data remains server-side, assuming a short page means "done"
// would silently truncate the sync — dropping every resource beyond it while reporting
// success. So a short page only stops pagination if Total also confirms there's
// nothing left; otherwise it keeps going, costing at most one extra request per page
// that came back short, never an infinite loop, since the walk still terminates the
// moment either signal above fires (worst case, one final empty page).
func getClmNextToken(requestOffset, itemCount, total int) string {
	if itemCount == 0 {
		return ""
	}
	nextOffset := requestOffset + itemCount
	if total > 0 && nextOffset >= total {
		return ""
	}
	return encodeClmPageToken(&clmPageToken{Offset: nextOffset})
}
