package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
// the account's hourly API-call budget being exhausted — confirmed against a real account.
// The published eSignature "rules and resource limits" / error-codes pages quote the
// human message (see docusignHourlyRateLimitErrorMessage) and related envelope-scoped
// codes (e.g. Hourly_APIInvocation_Envelope_Limit_Exceeded), but do not list this exact
// account-level errorCode string; detection matches either. DocuSign returns this as
// HTTP 400 today and is mid-migration to 429 (DocuSign's own guidance is to key off
// errorCode / the error body, not HTTP status), so detection below ignores StatusCode.
const docusignHourlyRateLimitErrorCode = "HOURLY_APIINVOCATION_LIMIT_EXCEEDED"

// docusignHourlyRateLimitErrorMessage is the account hourly-limit error text quoted by
// DocuSign's eSignature API rules-and-resource-limits docs ("If you exceed the API rate
// limit, you will receive the error: …"). Matched as a substring of ErrorResponse.message
// so a future errorCode rename still classifies correctly when the published message stays.
const docusignHourlyRateLimitErrorMessage = "The maximum number of hourly API invocations has been exceeded"

// docusignRateLimitDefaultResetWindow is the fixed ResetAt this connector puts on the
// RateLimitDescription for the account hourly-limit error — applied unconditionally,
// not just as a fallback (see reclassifyHourlyRateLimitError's doc for why response
// headers are not used for ResetAt here). The limit this error names is hourly, so an
// hour is the semantically correct value to report — but it is not what the SDK's retry
// loop actually waits: pkg/sync/parallel_syncer.go constructs its Retryer with
// MaxDelay: 0, which retry.NewRetryer normalizes to a 60-second cap, and
// retry.Retryer.ShouldWaitAndRetry computes a wait from this ResetAt only to then clamp
// it down to that same 60 seconds (`if wait > maxDelay { wait = maxDelay }`). With
// MaxAttempts: 0 (unlimited), the net effect is a 60-second retry with no attempt limit
// for the rest of the hour, not an hour of backoff — still strictly better than the old
// fatal classification, but not a full-hour wait. That gap lives in baton-sdk's retry
// package, not something this connector can change from here.
const docusignRateLimitDefaultResetWindow = time.Hour

// reclassifyHourlyRateLimitError recognizes the account hourly API-invocation limit in
// errTarget (the same *ErrorResponse instance uhttp.WithErrorResponse already unmarshaled
// the error body into before returning origErr — no re-parsing needed) and, if matched,
// returns a codes.Unavailable error carrying a RateLimitDescription. This matters because
// uhttp.GrpcCodeFromHTTPStatus maps this error's current HTTP 400 to codes.InvalidArgument,
// which the SDK's sync-retry loop (pkg/sync's Retryer, wired to SyncResourcesOp/
// SyncGrantsOp) does not retry — it only waits and retries on Unavailable/DeadlineExceeded,
// so an otherwise-recoverable rate limit was surfacing as a fatal, non-resumable sync
// failure. Returns nil (unchanged behavior) when errTarget isn't this specific eSignature
// error shape, or neither the live-confirmed errorCode nor the docs-quoted message match —
// including every ClmErrorResponse-based CLM call, which is a distinct error envelope
// this func never matches.
//
// Deliberately does NOT use ratelimit.ExtractRateLimitData's header-derived
// Limit/Remaining/ResetAt for ResetAt on this error. The eSignature rules-and-limits
// docs do map X-RateLimit-* to the account hourly budget and X-BurstLimit-* to the
// separate 30-second burst window — but on an already-over-limit response Remaining is
// uninformative, and trusting ResetAt/Remaining without knowing which limiter produced
// the body risks the SDK's Retryer (vendor pkg/retry/retry.go) computing a short wait
// off the burst window and hammering an account that's still over its hourly budget.
// Always uses the fixed hourly default window instead — safe by construction, if coarser.
func reclassifyHourlyRateLimitError(errTarget uhttp.ErrorResponse, origErr error) error {
	er, ok := errTarget.(*ErrorResponse)
	if !ok || !isHourlyAPIInvocationLimitError(er) {
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

// isHourlyAPIInvocationLimitError reports whether er is DocuSign's account-level hourly
// API-invocation budget error — matching the live-confirmed errorCode and/or the message
// text quoted in the eSignature rules-and-resource-limits docs.
func isHourlyAPIInvocationLimitError(er *ErrorResponse) bool {
	if er.ErrorCode == docusignHourlyRateLimitErrorCode {
		return true
	}
	return strings.Contains(er.ErrorMessage, docusignHourlyRateLimitErrorMessage)
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
