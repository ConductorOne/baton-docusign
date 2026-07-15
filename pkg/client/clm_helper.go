package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

// preparePagedRequestClm prepares a paged request URL using CLM's confirmed query
// param names (pageSortParams.offset/limit/...) — distinct from eSignature's
// start_position/count (see preparePagedRequest in helper.go).
func preparePagedRequestClm(baseURL *url.URL, endpoint string, options PageOptions) (*url.URL, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("baton-docusign: invalid CLM endpoint: %w", err)
	}

	fullURL := baseURL.ResolveReference(endpointURL)
	q := fullURL.Query()

	offset := 0
	if options.PageToken != "" {
		decoded, err := decodeClmPageToken(options.PageToken)
		if err != nil {
			return nil, fmt.Errorf("baton-docusign: invalid CLM page token: %w", err)
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
	return fullURL, nil
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
// Prefers the Offset+Limit<Total signal over parsing the Next URL, since Next's
// absolute-vs-relative shape is undocumented.
func getClmNextToken(page ClmPage) string {
	if page.Offset+page.Limit < page.Total {
		return encodeClmPageToken(&clmPageToken{Offset: page.Offset + page.Limit})
	}
	return ""
}
