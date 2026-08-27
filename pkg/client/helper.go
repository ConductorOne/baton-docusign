package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/ratelimit"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
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
//
// Matching is case-insensitive on both errorCode and the message substring (see
// isHourlyAPIInvocationLimitError): DocuSign's own published error codes are inconsistent
// on casing across endpoints (e.g. Hourly_APIInvocation_Envelope_Limit_Exceeded uses mixed
// case where this constant is all-caps), and this exact account-level code isn't in
// DocuSign's published list at all — so a future casing change on either field must not
// silently stop this predicate from matching.
const docusignHourlyRateLimitErrorCode = "HOURLY_APIINVOCATION_LIMIT_EXCEEDED"

// docusignHourlyRateLimitErrorMessage is the account hourly-limit error text quoted by
// DocuSign's eSignature API rules-and-resource-limits docs ("If you exceed the API rate
// limit, you will receive the error: …"). Matched as a case-insensitive substring of
// ErrorResponse.message so a future errorCode rename, or a casing change on either field,
// still classifies correctly as long as the published message text stays.
const docusignHourlyRateLimitErrorMessage = "The maximum number of hourly API invocations has been exceeded"

// docusignBurstRateLimitErrorCode is DocuSign's errorCode for the separate 30-second
// burst API-invocation window (distinct from the hourly account budget above) being
// exceeded — also HTTP 400 today. Unlike docusignHourlyRateLimitErrorCode, this exact
// string is a best-effort reconstruction of DocuSign's "BURST_APIINVOCATION_LIMIT_EXCEEDED"
// family (following the same naming pattern as the confirmed hourly code) rather than a
// value confirmed against a real account or a specific published docs page — matching is
// case-insensitive (see docusignHourlyRateLimitErrorCode's doc) specifically because this
// exact casing is not confirmed.
const docusignBurstRateLimitErrorCode = "BURST_APIINVOCATION_LIMIT_EXCEEDED"

// docusignBurstRateLimitErrorMessage is a best-effort guess at the human message DocuSign
// returns for the burst-limit error, matched as a case-insensitive substring the same way
// docusignHourlyRateLimitErrorMessage is. Not confirmed against DocuSign's published docs
// or a real account response — see docusignBurstRateLimitErrorCode's doc.
const docusignBurstRateLimitErrorMessage = "exceeded the burst limit"

// docusignRateLimitDefaultResetWindow is the fixed ResetAt this connector puts on the
// RateLimitDescription for the account hourly-limit error — applied unconditionally,
// not just as a fallback (see reclassifyRateLimitError's doc for why response headers are
// not used for ResetAt here). The limit this error names is hourly, so an hour is the
// semantically correct value to report — but it is not what the SDK's retry loop actually
// waits: pkg/sync/parallel_syncer.go constructs its Retryer with MaxDelay: 0, which
// retry.NewRetryer normalizes to a 60-second cap, and retry.Retryer.ShouldWaitAndRetry
// computes a wait from this ResetAt only to then clamp it down to that same 60 seconds
// (`if wait > maxDelay { wait = maxDelay }`). With MaxAttempts: 0 (unlimited), the net
// effect is a 60-second retry with no attempt limit for the rest of the hour, not an hour
// of backoff — still strictly better than the old fatal classification, but not a
// full-hour wait, and not free: every one of those 60-second retries is a real API call
// against an account that is already over its hourly budget, so an account that trips
// this limit early in the hour spends the rest of the hour retrying roughly once a minute
// (~60 wasted calls) while holding a sync slot open, instead of failing fast. That gap
// lives in baton-sdk's retry package, not something this connector can change from here.
const docusignRateLimitDefaultResetWindow = time.Hour

// docusignRateLimitBurstResetWindow is the fixed ResetAt window used for the burst-limit
// variant instead of docusignRateLimitDefaultResetWindow. DocuSign's burst window itself
// resets in 30 seconds; 45 seconds is used here (rather than 30 exactly) to stay safely
// above that boundary given clock skew and in-flight request latency between when
// DocuSign evaluated the limit and when this connector computes ResetAt.
const docusignRateLimitBurstResetWindow = 45 * time.Second

// reclassifyRateLimitError recognizes DocuSign's two eSignature API-invocation limit
// errors in errTarget (the same *ErrorResponse instance uhttp.WithErrorResponse already
// unmarshaled the error body into before returning origErr — no re-parsing needed) — the
// account's hourly call budget and the separate 30-second burst window — and, if either
// is matched, returns a codes.Unavailable error carrying a RateLimitDescription. This
// matters because uhttp.GrpcCodeFromHTTPStatus maps both errors' current HTTP 400 to
// codes.InvalidArgument, which the SDK's sync-retry loop (pkg/sync's Retryer, wired to
// SyncResourcesOp/SyncGrantsOp) does not retry — it only waits and retries on
// Unavailable/DeadlineExceeded, so an otherwise-recoverable rate limit was surfacing as a
// fatal, non-resumable sync failure. Returns nil (unchanged behavior) when errTarget isn't
// this specific eSignature error shape, or neither variant's errorCode/message match —
// including every ClmErrorResponse-based CLM call, which is a distinct error envelope
// this func never matches.
//
// Deliberately does NOT use ratelimit.ExtractRateLimitData's header-derived
// Limit/Remaining/ResetAt for ResetAt on either error. The eSignature rules-and-limits
// docs do map X-RateLimit-* to the account hourly budget and X-BurstLimit-* to the
// separate 30-second burst window — but on an already-over-limit response Remaining is
// uninformative, and trusting ResetAt/Remaining without knowing which limiter produced
// the body risks the SDK's Retryer (vendor pkg/retry/retry.go) computing a wait off the
// wrong limiter's header pair. Instead each variant gets its own fixed default window
// (docusignRateLimitDefaultResetWindow for hourly, docusignRateLimitBurstResetWindow for
// burst) — safe by construction, if coarser.
//
// On a match, logs a breadcrumb (via the request's logger, extracted from ctx) naming
// which variant matched, so a future DocuSign casing/wording change that stops this
// predicate from matching is observable as an absence of this log line rather than a
// silent revert to fatal sync failures.
//
// This is pkg/client's only logging call — every other log line in this connector lives
// in pkg/connector, one layer up, with full sync/policy context. Deliberate exception
// here: doRequestCommon is the one place that sees every DocuSign call regardless of
// which pkg/connector builder issued it, so this is the only spot a single log line can
// reliably catch every match; pushing it up a layer would mean adding the same call at
// every builder that might hit this error, with no way to guarantee none are missed.
func reclassifyRateLimitError(ctx context.Context, errTarget uhttp.ErrorResponse, origErr error) error {
	er, ok := errTarget.(*ErrorResponse)
	if !ok {
		return nil
	}

	var (
		kind   string
		window time.Duration
	)
	switch {
	case isHourlyAPIInvocationLimitError(er):
		kind, window = "hourly", docusignRateLimitDefaultResetWindow
	case isBurstAPIInvocationLimitError(er):
		kind, window = "burst", docusignRateLimitBurstResetWindow
	default:
		return nil
	}

	st := status.New(codes.Unavailable, origErr.Error())
	withDetails, detailsErr := st.WithDetails(v2.RateLimitDescription_builder{
		Status:  v2.RateLimitDescription_STATUS_OVERLIMIT,
		ResetAt: timestamppb.New(time.Now().Add(window)),
	}.Build())

	reclassified := withDetails.Err()
	if detailsErr != nil {
		// WithDetails only fails for a codes.OK status or a detail that can't marshal to
		// an Any — neither applies here (fixed codes.Unavailable, a well-formed proto
		// message) — but fall back to the plain Unavailable classification (still
		// retryable) rather than losing that reclassification entirely if it somehow does.
		reclassified = st.Err()
	}

	ctxzap.Extract(ctx).Info(
		"baton-docusign: reclassified DocuSign API-invocation rate-limit error as retryable",
		zap.String("rate_limit_kind", kind),
		zap.String("error_code", er.ErrorCode),
		zap.Duration("reset_window", window),
	)

	// Join, rather than discard, origErr: the new status carries the message/details this
	// reclassification needs, but origErr may itself be a joined error (WrapErrorsWithRateLimitInfo
	// joins the base status with every DoOption error, including header-derived rate-limit
	// data) whose value — not just its string form — future callers may rely on, e.g. via
	// errors.As. status.Code/status.FromError still resolve to the new codes.Unavailable
	// status via errors.As over the join tree (see TestReclassifyRateLimitError).
	return errors.Join(reclassified, origErr)
}

// matchesRateLimitVariant reports whether er matches a given DocuSign rate-limit error
// shape, by errorCode or message substring, case-insensitively on both — shared by
// isHourlyAPIInvocationLimitError and isBurstAPIInvocationLimitError so the two variants'
// matching logic can't drift apart.
func matchesRateLimitVariant(er *ErrorResponse, code, message string) bool {
	if strings.EqualFold(er.ErrorCode, code) {
		return true
	}
	return containsFold(er.ErrorMessage, message)
}

// isHourlyAPIInvocationLimitError reports whether er is DocuSign's account-level hourly
// API-invocation budget error — matching the live-confirmed errorCode and/or the message
// text quoted in the eSignature rules-and-resource-limits docs, case-insensitively on
// both (see docusignHourlyRateLimitErrorCode's doc for why).
func isHourlyAPIInvocationLimitError(er *ErrorResponse) bool {
	return matchesRateLimitVariant(er, docusignHourlyRateLimitErrorCode, docusignHourlyRateLimitErrorMessage)
}

// isBurstAPIInvocationLimitError reports whether er is DocuSign's separate 30-second
// burst API-invocation limit error — matching errorCode and/or message, case-insensitively
// on both (see docusignBurstRateLimitErrorCode's doc for why, and for the caveat that
// these exact strings are a best-effort reconstruction, not a confirmed value).
func isBurstAPIInvocationLimitError(er *ErrorResponse) bool {
	return matchesRateLimitVariant(er, docusignBurstRateLimitErrorCode, docusignBurstRateLimitErrorMessage)
}

// containsFold reports whether substr appears within s, ignoring case.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// IDFromHref extracts the trailing path segment from a CLM object's Href — CLM's
// Object API schemas expose a Href field ("Uri where the object can be retrieved") but
// no separate opaque Id field, so this is the closest thing to a native ID CLM exposes.
// Shared by pkg/connector (reading real Hrefs) and pkg/client/clmtest (generating seed
// Hrefs for the mock server), which otherwise each maintained an identical copy.
func IDFromHref(href string) string {
	href = strings.TrimSuffix(href, "/")
	if idx := strings.LastIndex(href, "/"); idx != -1 {
		return href[idx+1:]
	}
	return href
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
// On the error path, two specific eSignature errors (DocuSign's hourly API-call-budget
// error and its separate 30-second burst-limit error — see reclassifyRateLimitError) have
// their gRPC code silently overridden from whatever uhttp.GrpcCodeFromHTTPStatus would
// otherwise produce to codes.Unavailable, so the SDK's sync-retry loop treats them as
// retryable instead of fatal. Every other error is returned unchanged.
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
			if rlErr := reclassifyRateLimitError(req.Context(), errTarget, err); rlErr != nil {
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
