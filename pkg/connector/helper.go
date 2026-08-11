package connector

import (
	"strings"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Shared profile/field map keys, reused across builders (and the AccountCreationSchema
// field map in connector.go) to avoid repeated string literals (golangci-lint: goconst).
const (
	profileFieldEmail      = "email"
	profileFieldUsername   = "username"
	profileFieldGroupName  = "group_name"
	profileFieldPermission   = "permission"
	profileFieldStatus       = "status"
	profileFieldPermissionID = "permission_profile_id"
)

// userStatusActive is the DocuSign UserStatus value this connector treats as "active" —
// used to gate userBuilder.Grants' list-response fast path on the same active/non-active
// distinction GetUserDetails' PermissionProfileID-empty check already relies on (see
// users.go), not a new assumption.
const userStatusActive = "Active"

// isReclassifiedRateLimitError reports whether err represents a genuine rate-limit
// overlimit — either DocuSign's hourly error (pkg/client/helper.go's
// reclassifyHourlyRateLimitError) or a plain HTTP 429 uhttp's own
// WrapErrorsWithRateLimitInfo already classifies this way — identified by a
// RateLimitDescription with Status == STATUS_OVERLIMIT specifically, not merely the
// presence of a RateLimitDescription at all: uhttp's wrapper.go attaches one to every
// non-2xx response unconditionally (ratelimit.ExtractRateLimitData never errors, so
// WrapErrorsWithRateLimitInfo's `if err == nil { st.WithDetails(description) }` always
// runs), almost always with Status left at its unset zero value — so a bare presence
// check would false-positive-match ordinary unrelated errors (403s, validation errors,
// anything non-2xx). codes.Unavailable alone is also too broad on its own: uhttp maps a
// plain 503 or a transient network failure to it too, and those are genuinely different
// failures a caller may want to handle differently (e.g. still fall back to another
// endpoint) rather than treat as an account already over its rate-limit budget.
func isReclassifiedRateLimitError(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	for _, d := range st.Details() {
		if rl, ok := d.(*v2.RateLimitDescription); ok && rl.GetStatus() == v2.RateLimitDescription_STATUS_OVERLIMIT {
			return true
		}
	}
	return false
}

// parsePageToken deserializes the Baton token and returns the Bag and page number for upstream.
func parsePageToken(i string, resourceID *v2.ResourceId) (*pagination.Bag, string, error) {
	b := &pagination.Bag{}
	if err := b.Unmarshal(i); err != nil {
		return nil, "", err
	}

	if b.Current() == nil {
		b.Push(pagination.PageState{
			ResourceTypeID: resourceID.ResourceType,
			ResourceID:     resourceID.Resource,
		})
	}

	return b, b.PageToken(), nil
}

// isOptInFeatureUnavailableError reports whether err indicates this account/token
// simply can't use an optional DocuSign feature — no subscription (CLM), the feature
// isn't enabled on the account (signing groups), or the OAuth token lacks the scopes it
// needs (CLM's spring_read/spring_write — see oauth.go) — rather than an unexpected
// failure.
//
// The 5 CLM resource types (and signing_group's List() has the same shape of check)
// are registered unconditionally in ResourceSyncers() and their List() bodies always
// run, with no config flag gating them, specifically so that a resource type never
// disappears from a later sync and gets treated as fully deleted. Tolerating this error
// on the first page of List() (see call sites) is what makes unconditional registration
// safe: the sync skips that one resource type gracefully instead of failing outright.
//
// Covers four codes, each tied to a specific confirmed failure mode of
// ensureClmInitialized's CLM base-URL discovery call (clm_client.go) — the first thing
// every CLM builder's List() does, now unconditionally:
//   - PermissionDenied/Unauthenticated: the account/token lacks the CLM subscription
//     or OAuth scope — the expected case for most eSignature-only accounts.
//   - NotFound: the discovery endpoint 404s for an account that was never provisioned
//     in the legacy SpringCM system CLM discovery still runs through.
//   - FailedPrecondition: ensureClmInitialized wraps its "response didn't contain a
//     recognized base-URL field" error with this code specifically — a non-CLM
//     account's discovery response plausibly has a different shape entirely (no CLM
//     fields at all), which would otherwise surface as an unrecognized codes.Unknown
//     and fail the whole sync.
//
// Deliberately still doesn't cover codes.Unknown itself (an un-coded, unwrapped error)
// or 5xx/transport failures (codes.Unavailable/DeadlineExceeded/etc.) — those stay
// loud, since they're as likely to indicate a real outage or bug as a no-CLM account,
// and swallowing them broadly would hide genuine failures. Every other resource type
// (user, group, permission_profile) is always attempted and does not tolerate this
// error at all, so a truly broken token still fails the sync via those.
func isOptInFeatureUnavailableError(err error) bool {
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

// permissionProfileIDByName returns the ID of the permission profile named name, and
// whether one was found — shared by userBuilder.Grants' list-response fast path and
// permissionProfilesBuilder.Revoke's default-profile lookup, which independently
// duplicated this same linear scan before this helper existed. Requires a non-empty ID,
// matching Revoke's original inline check (`if defaultProfileID == ""`) that a name match
// with no usable ID counts as not found, not as an empty-string result.
func permissionProfileIDByName(profiles []client.PermissionProfile, name string) (string, bool) {
	for _, p := range profiles {
		if p.PermissionProfileName == name && p.PermissionProfileId != "" {
			return p.PermissionProfileId, true
		}
	}
	return "", false
}

// clmIDFromHref extracts the trailing path segment from a CLM object's Href — CLM's
// Object API schemas expose a Href field ("Uri where the object can be retrieved") but
// no separate opaque Id field, so this is the closest thing to a native ID CLM exposes.
func clmIDFromHref(href string) string {
	href = strings.TrimSuffix(href, "/")
	if idx := strings.LastIndex(href, "/"); idx != -1 {
		return href[idx+1:]
	}
	return href
}
