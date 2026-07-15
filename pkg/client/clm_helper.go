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
// requested alongside the URL, so callers can compute the next-page token from what was
// actually asked for.
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

// getClmNextToken calculates the next-page token from a CLM collection response.
// Computed from requestOffset (what we actually asked for) plus the number of items
// actually returned — not the response's own Offset field. Trusting the response to
// echo the requested offset back correctly was never confirmed live: if it doesn't,
// this would compute the same non-empty token forever, looping the SDK's own
// pagination driver indefinitely across List()/Grants() calls, which has no built-in
// non-advancing-token guard of its own (unlike GetMemberGroups' internal pagination
// loop, which explicitly checks for and bails out on a non-advancing token).
func getClmNextToken(requestOffset, itemCount, total int) string {
	if itemCount == 0 {
		return ""
	}
	nextOffset := requestOffset + itemCount
	if nextOffset >= total {
		return ""
	}
	return encodeClmPageToken(&clmPageToken{Offset: nextOffset})
}
