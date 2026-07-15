package connector

import (
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
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
