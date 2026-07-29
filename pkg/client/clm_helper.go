package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// clmRequestedPage records what a paged CLM request actually asked for — offset and
// page size — so the response can be paginated against ground truth instead of trusting
// response fields that were never confirmed to be populated reliably (see
// getClmNextToken). Page is this request's position in the pagination sequence (0 for
// the first page), carried through so getClmNextToken can enforce maxClmListPages.
type clmRequestedPage struct {
	Offset   int
	PageSize int
	Page     int
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
	page := 0
	if options.PageToken != "" {
		decoded, err := decodeClmPageToken(options.PageToken)
		if err != nil {
			return nil, clmRequestedPage{}, fmt.Errorf("baton-docusign: invalid CLM page token: %w", err)
		}
		offset = decoded.Offset
		page = decoded.Page
	}
	q.Set("pageSortParams.offset", strconv.Itoa(offset))

	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	q.Set("pageSortParams.limit", strconv.Itoa(pageSize))

	fullURL.RawQuery = q.Encode()
	return fullURL, clmRequestedPage{Offset: offset, PageSize: pageSize, Page: page}, nil
}

// clmPageToken is the internal offset-based continuation token for CLM pagination.
// Page counts how many pages have been fetched so far in this sequence — see
// maxClmListPages.
type clmPageToken struct {
	Offset int `json:"offset"`
	Page   int `json:"page"`
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
// a positive Total signal was getting silently ignored.
//
// Conscious, documented tradeoff (per review discussion): the totalSaysMoreRemains
// override reintroduces a bounded version of the out-of-range-probe risk that motivated
// the earlier revert away from always trusting Total. If Total over-reports (says more
// remains when it doesn't), this issues one extra request at an offset with no data —
// harmless if the endpoint returns an empty 200 there, but would fail the sync if it
// 4xxs on an out-of-range offset instead. That's strictly bounded to one extra request
// (never a loop) and only reachable when Total is both populated and wrong, which is
// narrower and safer than the previously-reverted version (which extended this same
// risk to every short page whenever Total was merely unpopulated). No live CLM tenant
// was available to confirm Total's accuracy either way, so this residual risk is
// accepted rather than resolved.
//
// maxClmListPages bounds the total pages any single SDK-driven List() pagination
// sequence can walk (SearchFolders/ListGroups/GetGroupMembers/ListMembers/
// ListPermissionSets, plus GetMemberGroups' own internal loop, which already had this
// exact protection independently — see maxMemberGroupPages in clm_client.go). Without
// it, a CLM endpoint that ignores pageSortParams.offset (always returning the same
// full first page) combined with an unpopulated Total would make nextOffset advance
// forever in our own accounting while the server-side data never actually changes —
// unlike GetMemberGroups, these paths can't compare successive responses for
// non-advancement (the SDK drives one page per List() call, with no request-to-request
// memory beyond the token), so a hard page cap is the only local safeguard available.
// Reaching it fails the sync loudly rather than paginating indefinitely.
const maxClmListPages = 1000

func getClmNextToken(requested clmRequestedPage, itemCount int, hasNext bool, total int) (string, error) {
	if itemCount == 0 {
		return "", nil
	}
	nextOffset := requested.Offset + itemCount
	if total > 0 && nextOffset >= total {
		return "", nil
	}
	totalSaysMoreRemains := total > 0 && nextOffset < total
	if itemCount < requested.PageSize && !hasNext && !totalSaysMoreRemains {
		return "", nil
	}

	nextPage := requested.Page + 1
	if nextPage >= maxClmListPages {
		return "", fmt.Errorf("baton-docusign: exceeded %d pages paginating a CLM list — the API may be ignoring the requested offset", maxClmListPages)
	}

	return encodeClmPageToken(&clmPageToken{Offset: nextOffset, Page: nextPage}), nil
}
