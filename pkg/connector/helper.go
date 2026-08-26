package connector

import (
	"context"
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
	profileFieldHref      = "href"
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

// clmIDFromHref extracts the trailing path segment from a CLM object's Href — see
// client.IDFromHref's doc. pkg/client/clmtest can't import pkg/connector, so the single
// definition lives in pkg/client and both packages delegate to it instead of
// maintaining two copies.
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
		return "", status.Errorf(codes.InvalidArgument, "baton-docusign: cannot derive a sibling href from %q — newID is empty", sampleHref)
	}
	// Split on the parsed URL's Path, not the raw string: a "/" inside a query string or
	// fragment (e.g. ".../groups/g1?filter=a/b") would otherwise win over the real path
	// separator, corrupting the query instead of replacing the ID segment. A bare
	// scheme+host like "https://clm.example.com" parses with an empty Path, so this also
	// covers that case — no real path at all to derive from.
	u, err := url.Parse(sampleHref)
	if err != nil || u.Path == "" {
		return "", status.Errorf(codes.InvalidArgument, "baton-docusign: cannot derive a sibling href from %q — no path segment found", sampleHref)
	}
	// A Path already ending in "/" has no ID segment to replace — the same degenerate
	// shape the empty-newID check above guards against on the output side. Trimming it
	// instead of rejecting it would silently drop the real collection segment: e.g.
	// ".../groups/" trims to ".../groups", and the LastIndex split below would then
	// wrongly treat "groups" as the ID to replace, producing ".../<newID>" with the
	// actual collection segment gone.
	if u.Path == "/" || strings.HasSuffix(u.Path, "/") {
		return "", status.Errorf(codes.InvalidArgument, "baton-docusign: cannot derive a sibling href from %q — sample has no ID segment (trailing slash)", sampleHref)
	}
	idx := strings.LastIndex(u.Path, "/")
	if idx == -1 {
		// A relative, scheme-less sample (e.g. "no-path-separator") parses as an opaque
		// Path with no leading "/" at all — url.Parse doesn't error on it, so this catches
		// what the raw-string check used to.
		return "", status.Errorf(codes.InvalidArgument, "baton-docusign: cannot derive a sibling href from %q — no path separator found", sampleHref)
	}
	u.Path = u.Path[:idx+1] + newID
	// Clear any query/fragment carried over from the sample — the derived href
	// identifies a different object than the sample's, so a leftover query string or
	// fragment (e.g. ".../group-old?filter=a/b" deriving ".../group-new?filter=a/b")
	// would misrepresent the sample's, not the target's, state in a write body.
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
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
// codebase's assumptions don't cover, so it's logged at Debug before falling back — not
// visible by default, but distinguishable from the routine case for anyone who does
// raise verbosity, rather than discarded with no trace at all.
func clmPreferredHref(ctx context.Context, id string, sampleHrefs []string, deriveFallback func() (string, error)) (string, error) {
	if id == "" {
		// clmHrefWithID rejects an empty id on the sample path, but deriveFallback (a
		// caller-supplied closure, typically client.GroupHref/MemberHref built from this
		// same id) has no such guard — an empty id would sail through it and produce the
		// same malformed trailing-slash href this function exists to prevent. Reject here
		// once, before either path runs, rather than relying on every deriveFallback
		// closure to check it independently.
		return "", status.Errorf(codes.InvalidArgument, "baton-docusign: cannot resolve a CLM href — id is empty")
	}
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
	} else {
		// No samples at all (as opposed to samples that failed to parse, above) — the
		// routine case for e.g. a group/member with no other memberships yet. Still
		// worth a Debug line: deriveFallback's shape is unverified against a live
		// tenant, so if CLM ever rejects or silently ignores the derived href, this is
		// the only record that a guess (not a real sample) produced it.
		ctxzap.Extract(ctx).Debug("baton-docusign: no sample href available, falling back to a base-URL-derived href",
			zap.String("id", id))
	}
	return deriveFallback()
}

// clmSampleHrefsFrom builds the sampleHrefs slice clmPreferredHref expects: principal's
// own profile href first (the most direct sample when the resource happens to carry
// one — only ever a sample, never required, so an identity-only principal still falls
// through to the entries/fallback path unchanged), followed by every entry's Href.
func clmSampleHrefsFrom[T any](principal *v2.Resource, entries []T, hrefOf func(T) string) []string {
	sampleHrefs := make([]string, 0, len(entries)+1)
	// GetProfileStringValue returns ok == true for a present-but-empty "href" key
	// (every parseIntoClm*Resource always writes it), so this must also reject "" —
	// otherwise an absent-sample account looks identical to an unexpected-shape one:
	// the empty string always fails clmHrefWithID, tripping clmPreferredHref's
	// unexpected-failure log on what is actually the routine no-sample case.
	if href, ok := rs.GetProfileStringValue(rs.GetProfile(principal), profileFieldHref); ok && href != "" {
		sampleHrefs = append(sampleHrefs, href)
	}
	for _, e := range entries {
		// Same reasoning as the profile href above: an empty entry Href would fail
		// clmHrefWithID the same way and re-trip the unexpected-failure log on
		// otherwise-routine degenerate data.
		if href := hrefOf(e); href != "" {
			sampleHrefs = append(sampleHrefs, href)
		}
	}
	return sampleHrefs
}

// logSkippedClmWorkflowQueueWithEmptyHref Debug-logs a workflow-queue entry whose Href
// does not resolve to a usable native ID — the queue is skipped rather than synced/granted
// with an empty resource ID.
func logSkippedClmWorkflowQueueWithEmptyHref(ctx context.Context, memberID, queueHref string) {
	ctxzap.Extract(ctx).Debug("baton-docusign: skipping CLM workflow queue with an empty Href-derived ID",
		zap.String("member_id", memberID), zap.String("queue_href", queueHref))
}
