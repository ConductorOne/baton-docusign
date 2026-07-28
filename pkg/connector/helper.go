package connector

import (
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
// needs (CLM's spring_read/spring_write, gated by include-clm — see oauth.go) — rather
// than an unexpected failure.
//
// The opt-in resource types (the 5 CLM types, signing_group) are registered
// unconditionally in ResourceSyncers() rather than gated by a config flag, specifically
// so that disabling a flag never makes C1 see a resource type disappear and treat every
// previously-synced resource/grant of that type as deleted. Tolerating this error on
// the first page of List() (see call sites) is what makes unconditional registration
// safe: the sync skips that one resource type gracefully instead of failing outright.
// It's intentionally narrow (PermissionDenied/Unauthenticated only, and only checked on
// the first page) so a genuine credential problem still fails loudly — every other
// resource type (user, group, permission_profile) is always attempted and does not
// tolerate this error, so a truly broken token still fails the sync via those.
func isOptInFeatureUnavailableError(err error) bool {
	code := status.Code(err)
	return code == codes.PermissionDenied || code == codes.Unauthenticated
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
