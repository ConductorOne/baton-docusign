package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/ratelimit"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const DefaultPageSize = 100

// docusignHourlyRateLimitErrorCode is the eSignature API's JSON error-body errorCode for
// "the account's hourly API-call budget is exhausted" — confirmed against a real account.
// DocuSign returns this as HTTP 400 today and is mid-migration to 429 (DocuSign's own
// guidance is to key off errorCode, not HTTP status, for exactly this reason), so
// detection below checks the body field independent of resp.StatusCode.
const docusignHourlyRateLimitErrorCode = "HOURLY_APIINVOCATION_LIMIT_EXCEEDED"

// docusignRateLimitDefaultResetWindow is the fixed wait this connector asks the SDK's
// retry loop to use for docusignHourlyRateLimitErrorCode — applied unconditionally, not
// just as a fallback (see reclassifyHourlyRateLimitError's doc for why response headers
// are deliberately never consulted for this error). The limit this error names is
// hourly, so an hour is the sane, safe choice.
const docusignRateLimitDefaultResetWindow = time.Hour

// reclassifyHourlyRateLimitError recognizes docusignHourlyRateLimitErrorCode in errTarget (the
// same *ErrorResponse instance uhttp.WithErrorResponse already unmarshaled the error body
// into before returning origErr — no re-parsing needed) and, if matched, returns a
// codes.Unavailable error carrying a RateLimitDescription. This matters because
// uhttp.GrpcCodeFromHTTPStatus maps this error's current HTTP 400 to codes.InvalidArgument,
// which the SDK's sync-retry loop (pkg/sync's Retryer, wired to SyncResourcesOp/
// SyncGrantsOp) does not retry — it only waits and retries on Unavailable/DeadlineExceeded,
// so an otherwise-recoverable rate limit was surfacing as a fatal, non-resumable sync
// failure. Returns nil (unchanged behavior) when errTarget isn't this specific eSignature
// error shape, or the errorCode doesn't match — including every ClmErrorResponse-based CLM
// call, which is a distinct error envelope this func never matches.
//
// Deliberately does NOT read ratelimit.ExtractRateLimitData's header-derived
// Limit/Remaining/ResetAt for this specific error: DocuSign's docs describe no dedicated
// headers for this hourly/daily-scoped limit (detection has to go through the error body
// at all), so any generic X-RateLimit-*/Ratelimit-* headers present on this response most
// plausibly describe an unrelated shorter-window limit (e.g. a burst counter), not the
// hourly one that actually produced this error. Trusting them anyway risks the SDK's
// Retryer (vendor pkg/retry/retry.go) computing a short wait off a nonzero Remaining from
// the wrong bucket and hammering an account that's still over its hourly budget. Always
// uses the fixed hourly default window instead — safe by construction, if coarser.
func reclassifyHourlyRateLimitError(errTarget uhttp.ErrorResponse, origErr error) error {
	er, ok := errTarget.(*ErrorResponse)
	if !ok || er.ErrorCode != docusignHourlyRateLimitErrorCode {
		return nil
	}

	st := status.New(codes.Unavailable, origErr.Error())
	withDetails, detailsErr := st.WithDetails(v2.RateLimitDescription_builder{
		Status:  v2.RateLimitDescription_STATUS_OVERLIMIT,
		ResetAt: timestamppb.New(time.Now().Add(docusignRateLimitDefaultResetWindow)),
	}.Build())
	if detailsErr != nil {
		// WithDetails only fails for a codes.OK status or a detail that can't marshal to
		// an Any — neither applies here (fixed codes.Unavailable, a well-formed proto
		// message) — but fall back to the plain Unavailable classification (still
		// retryable) rather than losing that reclassification entirely if it somehow does.
		return st.Err()
	}
	return withDetails.Err()
}

// BuildURL combines the base API URL with a formatted endpoint path.
func buildURL(base, path string, params ...any) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	formatted := fmt.Sprintf(path, params...)
	endpoint, err := url.Parse(formatted)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint path: %w", err)
	}
	return baseURL.ResolveReference(endpoint), nil
}

// DoRequestCommon executes the HTTP request and handles rate limit annotations.
// errTarget receives the parsed error body on non-2xx responses (e.g. &ErrorResponse{}
// for eSignature, &ClmErrorResponse{} for CLM) since the two APIs use different error envelopes.
//
// On the error path, one specific eSignature error (DocuSign's hourly API-call-budget
// error — see reclassifyHourlyRateLimitError) has its gRPC code silently overridden from
// whatever uhttp.GrpcCodeFromHTTPStatus would otherwise produce to codes.Unavailable, so
// the SDK's sync-retry loop treats it as retryable instead of fatal. Every other error is
// returned unchanged.
func doRequestCommon(wrapper *uhttp.BaseHttpClient, req *http.Request, res any, errTarget uhttp.ErrorResponse) (http.Header, annotations.Annotations, error) {
	opts := []uhttp.DoOption{}
	if res != nil {
		opts = append(opts, uhttp.WithJSONResponse(res))
	}
	opts = append(opts, uhttp.WithErrorResponse(errTarget))
	resp, err := wrapper.Do(req, opts...)
	if err != nil {
		// resp is non-nil here whenever the error came from a well-formed non-2xx HTTP
		// response (as opposed to a network/transport failure) — see wrapper.Do.
		if resp != nil {
			if rlErr := reclassifyHourlyRateLimitError(errTarget, err); rlErr != nil {
				return resp.Header, nil, rlErr
			}
		}
		return nil, nil, err
	}
	defer resp.Body.Close()

	ann := annotations.Annotations{}
	if desc, err := ratelimit.ExtractRateLimitData(resp.StatusCode, &resp.Header); err == nil {
		ann.WithRateLimiting(desc)
	}
	return resp.Header, ann, nil
}

// EncodePageToken serializes pageToken to a base64 string.
func encodePageToken(pt *pageToken) string {
	b, _ := json.Marshal(pt)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodePageToken deserializes a base64 token string back into a pageToken struct.
func decodePageToken(token string) (*pageToken, error) {
	if token == "" {
		return &pageToken{StartPosition: 0}, nil
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	var pt pageToken
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, err
	}
	return &pt, nil
}

// PreparePagedRequest prepares the URL for a paged request.
func preparePagedRequest(baseURL *url.URL, endpoint string, options PageOptions) (*url.URL, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}

	fullURL := baseURL.ResolveReference(endpointURL)
	q := fullURL.Query()
	if options.PageToken != "" {
		pt, err := decodePageToken(options.PageToken)
		if err != nil {
			return nil, fmt.Errorf("invalid page token: %w", err)
		}
		q.Set("start_position", fmt.Sprintf("%d", pt.StartPosition))
	} else {
		q.Set("start_position", "0")
	}

	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	q.Set("count", fmt.Sprintf("%d", pageSize))

	fullURL.RawQuery = q.Encode()
	return fullURL, nil
}

// GetNextToken calculates the token for the next page based on the response.
func getNextToken(responsePage Page) string {
	// responsePage.EndPosition will always be 1 position behind the actual end of the list.
	// So we must add 1 to validate if we are at the last value.
	if responsePage.EndPosition+1 < responsePage.TotalSetSize {
		return encodePageToken(&pageToken{
			StartPosition: responsePage.EndPosition + 1,
		})
	}
	return ""
}

func ApplyQueryParam(reqURL *url.URL, key string, value string) {
	q := reqURL.Query()
	q.Set(key, value)
	reqURL.RawQuery = q.Encode()
}
