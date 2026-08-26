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
// getClmNextToken). Requests is how many requests this pagination sequence has made so
// far (0 for the first) — see maxClmListPages for why this is combined with, not used
// instead of, an offset-derived estimate.
type clmRequestedPage struct {
	Offset   int
	PageSize int
	Requests int
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

	return appendClmPageQuery(baseURL.ResolveReference(endpointURL), options)
}

// appendClmPageQuery appends CLM's pageSortParams.offset/limit query params to an
// already-resolved absolute URL and decodes options.PageToken — the part of
// preparePagedRequestClm that doesn't depend on resolving a relative endpoint against
// the CLM base URL. Split out for SearchFolders' continuation pages, which paginate
// against a server-issued Result href (a per-search URL CLM hands back, not one of this
// package's own static endpoint constants) rather than a fixed collection endpoint.
func appendClmPageQuery(fullURL *url.URL, options PageOptions) (*url.URL, clmRequestedPage, error) {
	q := fullURL.Query()

	offset := 0
	requests := 0
	if options.PageToken != "" {
		decoded, err := decodeClmPageToken(options.PageToken)
		if err != nil {
			return nil, clmRequestedPage{}, fmt.Errorf("baton-docusign: invalid CLM page token: %w", err)
		}
		offset = decoded.Offset
		requests = decoded.Requests
	}
	q.Set("pageSortParams.offset", strconv.Itoa(offset))

	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	q.Set("pageSortParams.limit", strconv.Itoa(pageSize))

	fullURL.RawQuery = q.Encode()
	return fullURL, clmRequestedPage{Offset: offset, PageSize: pageSize, Requests: requests}, nil
}

// clmPageToken is the internal offset-based continuation token for CLM pagination.
// Requests counts how many requests this pagination sequence has made so far — see
// maxClmListPages. ResultHref is only set by SearchFolders' continuation pages: unlike
// every other CLM list endpoint (a fixed collection URL re-queried with a different
// offset), a folder search's results live at a per-search URL CLM hands back from the
// FolderSearchTasks create call, so the token must carry it forward — the collection
// URL isn't otherwise derivable from the resource type alone.
type clmPageToken struct {
	Offset     int    `json:"offset"`
	Requests   int    `json:"requests"`
	ResultHref string `json:"resultHref,omitempty"`
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
//
// The page count used against maxClmListPages is the larger of two independent
// estimates, not either one alone:
//
//   - requested.Requests: an actual count of requests made so far in this sequence,
//     carried in the token. Correctly counts a pathological run of short pages kept
//     alive by hasNext/totalSaysMoreRemains (see above) — nextOffset alone would
//     undercount there, advancing by less than PageSize per request, letting a
//     runaway short-page sequence go up to PageSize times longer than intended before
//     tripping.
//   - nextOffset/requested.PageSize: recoverable from Offset alone with no history,
//     including from a token minted before this cap existed, or one round-tripped by
//     something that dropped an unrecognized field. requested.Requests would silently
//     reset to 0 in either case, defeating the cap exactly when a runaway sequence is
//     most likely to have started (e.g. a resumed sync) — this floor still catches it.
//
// Taking the max of both means neither weakness compounds: a healthy, large sync where
// every page is full advances both signals together; a pathological short-page sequence
// is caught by requested.Requests; a resumed sequence with no counter history is caught
// by the offset floor.
//
// Known, accepted limitations (per review discussion — consciously not fixed, so the
// eventual "exceeded 1000 pages" support ticket has a pointer here):
//
//   - This cannot distinguish a genuinely non-advancing endpoint from a legitimately
//     huge tenant. With DefaultPageSize=100, that's a hard ceiling of ~100k objects in
//     any single CLM type (folders being the most plausible one to approach it) before
//     an otherwise-healthy sync starts failing with an error that blames the API for
//     ignoring offsets. A narrower check (comparing the response's own echoed Offset,
//     or requested.Offset against Total, to detect non-advancement directly on page 2)
//     would avoid this, but this codebase already reverted trusting response.Offset
//     once in this same review cycle — none of Offset/Total/Next were ever confirmed
//     populated reliably against a live CLM tenant, so a comparison like that risks a
//     false positive on a healthy sync where the field is simply unpopulated, which is
//     a worse failure mode than the rare true positive it would catch sooner.
//   - The offset floor (nextOffset/PageSize) assumes PageSize is constant across the
//     whole pagination sequence. That holds within one run, but a sync resumed with a
//     different configured page size than the run that minted the carried-over token
//     would divide a large Offset by a different PageSize than it was accumulated
//     under, which can inflate or deflate the floor's estimate. requested.Requests is
//     unaffected by this (it counts requests, not offset distance), so this only
//     matters when the floor is the larger of the two estimates.
const maxClmListPages = 1000

// resultHref is embedded in the returned token as-is (see clmPageToken's doc) — pass ""
// for every endpoint except SearchFolders' continuation pages.
func getClmNextToken(requested clmRequestedPage, itemCount int, hasNext bool, total int, resultHref string) (string, error) {
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

	nextRequests := requested.Requests + 1
	pageEstimate := nextRequests
	if requested.PageSize > 0 {
		if floor := nextOffset / requested.PageSize; floor > pageEstimate {
			pageEstimate = floor
		}
	}
	if pageEstimate >= maxClmListPages {
		return "", fmt.Errorf("baton-docusign: exceeded %d pages paginating a CLM list — the API may be ignoring the requested offset", maxClmListPages)
	}

	return encodeClmPageToken(&clmPageToken{Offset: nextOffset, Requests: nextRequests, ResultHref: resultHref}), nil
}
