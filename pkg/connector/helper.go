package connector

import (
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Shared profile/field map keys, reused across builders (and the AccountCreationSchema
// field map in connector.go) to avoid repeated string literals (golangci-lint: goconst).
const (
	profileFieldEmail     = "email"
	profileFieldUsername  = "username"
	profileFieldGroupName = "group_name"
)

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

// clmHrefWithID rebuilds sampleHref with its trailing ID segment replaced by newID —
// used to derive a sibling object's Href from a known-real one, instead of guessing at
// the host from the discovered CLM base URL, whenever a real sample happens to be
// available. See clmPreferredHref for why this matters specifically for writes.
func clmHrefWithID(sampleHref, newID string) (string, error) {
	trimmed := strings.TrimSuffix(sampleHref, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx == -1 {
		return "", fmt.Errorf("baton-docusign: cannot derive a sibling href from %q — no path separator found", sampleHref)
	}
	return trimmed[:idx+1] + newID, nil
}

// clmPreferredHref resolves the href to send in a WRITE targeting id: it prefers
// deriving the href from a real, server-issued sample href (the first non-empty entry
// in sampleHrefs) over guessing at the host from the discovered CLM base URL. A write
// that carries a subtly-wrong host — if the discovered base URL ever differs from what
// CLM's own Href values actually use, since no live tenant confirmed this — risks CLM
// rejecting it or storing it inconsistently; a read-side comparison doesn't have this
// risk, since clmIDFromHref only ever looks at the trailing ID. Falls back to
// deriveFallback when no real sample href is available (e.g. a member with no other
// group memberships yet, or a folder with no other security entries yet).
func clmPreferredHref(id string, sampleHrefs []string, deriveFallback func() (string, error)) (string, error) {
	for _, sample := range sampleHrefs {
		if sample == "" {
			continue
		}
		if derived, err := clmHrefWithID(sample, id); err == nil {
			return derived, nil
		}
	}
	return deriveFallback()
}
