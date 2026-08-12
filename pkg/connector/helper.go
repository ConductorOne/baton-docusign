package connector

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
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
//     in the legacy SpringCM system. CLM's Object API returns 404 both for "doesn't
//     exist" and "exists but no access", so treat it as a plausible no-access signal,
//     not proof the account lacks CLM.
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

// clmIDFromHref extracts the trailing path segment from a CLM object's Href — see
// client.IDFromHref's doc. pkg/client/clmtest reimplements the same logic locally to
// avoid depending on pkg/connector, so both packages call the one shared definition in
// pkg/client instead of maintaining two copies.
func clmIDFromHref(href string) string {
	return client.IDFromHref(href)
}

// clmHrefWithID rebuilds sampleHref with its trailing ID segment replaced by newID.
// Assumes same-collection Hrefs share path shape (only the ID differs) — not verified
// against a live tenant. Callers must only pass samples from a single collection; newID
// must be non-empty (see the check below for why).
func clmHrefWithID(sampleHref, newID string) (string, error) {
	if newID == "" {
		// Without this check, an empty newID passes every shape check below (sampleHref
		// still has a valid path) and silently returns a trailing-slash href with no ID
		// segment at all — a malformed href that goes on to be sent as-is inside a
		// PatchFolderSecurity/PatchMemberGroups request body, surfacing (if at all) as an
		// opaque remote validation failure with no link back to "the ID was empty."
		return "", fmt.Errorf("baton-docusign: cannot derive a sibling href from %q — newID is empty", sampleHref)
	}
	trimmed := strings.TrimSuffix(sampleHref, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx == -1 {
		return "", fmt.Errorf("baton-docusign: cannot derive a sibling href from %q — no path separator found", sampleHref)
	}
	// A bare scheme+host like "https://clm.example.com" also contains a "/" (the one
	// separating scheme from host), so the LastIndex check above alone accepts it —
	// producing a garbage "https://<newID>" href with no real path. Reject a sample with
	// no path at all; a single-segment path like "https://host/group-old" is accepted.
	if u, err := url.Parse(trimmed); err != nil || u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("baton-docusign: cannot derive a sibling href from %q — no path segment found", sampleHref)
	}
	return trimmed[:idx+1] + newID, nil
}

// clmPreferredHref resolves the href to send in a WRITE targeting id: it prefers
// deriving the href from a real, server-issued sample href (the first entry in
// sampleHrefs that clmHrefWithID can use) over guessing at the host from the discovered
// CLM base URL. CLM's Href host is not guaranteed to match the discovered base URL — a
// write that carries a subtly-wrong host risks CLM rejecting it or storing it
// inconsistently; a read-side comparison doesn't have this risk, since clmIDFromHref
// only ever looks at the trailing ID. Falls back to deriveFallback when no real sample
// href is available (e.g. a member with no other group memberships yet, or a folder
// with no other security entries yet) — the expected, routine case (empty sampleHrefs
// never reaches clmHrefWithID at all). If sampleHrefs is non-empty but every sample
// fails to parse, that's the unexpected case: it means CLM returned a Href shape this
// codebase's assumptions don't cover, so it's logged before falling back, rather than
// silently masking exactly the wrong-host risk this function exists to avoid.
func clmPreferredHref(ctx context.Context, id string, sampleHrefs []string, deriveFallback func() (string, error)) (string, error) {
	var lastErr error
	for _, sample := range sampleHrefs {
		derived, err := clmHrefWithID(sample, id)
		if err == nil {
			return derived, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		ctxzap.Extract(ctx).Debug("baton-docusign: every sample href failed to derive a sibling href, falling back to a base-URL-derived href",
			zap.Int("sample_count", len(sampleHrefs)), zap.Error(lastErr))
	}
	return deriveFallback()
}

// clmSampleHrefsFrom builds the sampleHrefs slice clmPreferredHref expects: principal's
// own profile href first (the most direct sample when the resource happens to carry
// one — only ever a sample, never required, so an identity-only principal still falls
// through to the entries/fallback path unchanged), followed by every entry's Href.
func clmSampleHrefsFrom[T any](principal *v2.Resource, entries []T, hrefOf func(T) string) []string {
	sampleHrefs := make([]string, 0, len(entries)+1)
	if href, ok := rs.GetProfileStringValue(rs.GetProfile(principal), "href"); ok {
		sampleHrefs = append(sampleHrefs, href)
	}
	for _, e := range entries {
		sampleHrefs = append(sampleHrefs, hrefOf(e))
	}
	return sampleHrefs
}
