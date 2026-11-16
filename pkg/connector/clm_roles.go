package connector

import (
	"context"
	"fmt"
	"slices"
	"unicode"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/session"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
)

var (
	_ connectorbuilder.StaticEntitlementSyncerV2 = (*clmRoleBuilder)(nil)
	_ connectorbuilder.TypeScopedGrantsSyncer    = (*clmRoleBuilder)(nil)
)

const (
	clmRoleAssignedTag = "assigned"
	// clmRoleSessionPrefix namespaces the session keys that record which roles clm_role synced.
	clmRoleSessionPrefix = "clm-role"
	// clmMemberRolesSessionPrefix namespaces the clm_member List pages that clm_role grants read.
	clmMemberRolesSessionPrefix = "clm-member-roles"
)

// clmRoleBuilder syncs the client.ClmRoles values plus every role a CLM member holds; CLM has no roles endpoint.
type clmRoleBuilder struct {
	client *client.Client
	// includeMembers reports whether clm_member is synced; role grants point at member resources.
	includeMembers bool
	// includeFolders reports whether clm_folder is synced; only folder Grants read the roles List stores.
	includeFolders bool
}

func (b *clmRoleBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmRoleResourceType
}

// List emits the roles held by one page of members; the first page also emits client.ClmRoles.
// It reads the members list itself: the SDK doesn't order List calls across types, so clm_member's pages may not be stored yet.
func (b *clmRoleBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	members, nextPageToken, annos, err := b.client.ListMembers(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: attr.PageToken.Token,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: listing CLM members to discover roles: %w", err)
	}

	var roleNames []string
	if attr.PageToken.Token == "" {
		for _, role := range client.ClmRoles {
			roleNames = append(roleNames, role.Name)
		}
	}
	for _, member := range members {
		if member.Role != "" {
			roleNames = append(roleNames, member.Role)
		}
	}
	slices.Sort(roleNames)
	roleNames = slices.Compact(roleNames)

	resources := make([]*v2.Resource, 0, len(roleNames))
	syncedRoles := make(map[string]bool, len(roleNames))
	for _, role := range roleNames {
		roleResource, err := rs.NewRoleResource(clmRoleDisplayName(role), clmRoleResourceType, role, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("baton-docusign: creating CLM role resource %s: %w", role, err)
		}
		resources = append(resources, roleResource)
		syncedRoles[role] = true
	}

	if b.includeFolders {
		if err := session.SetManyJSON(ctx, attr.Session, syncedRoles, sessions.WithPrefix(clmRoleSessionPrefix)); err != nil {
			return nil, nil, fmt.Errorf("baton-docusign: storing synced CLM roles in the session store: %w", err)
		}
	}

	return resources, &rs.SyncOpResults{Annotations: annos, NextPageToken: nextPageToken}, nil
}

func (b *clmRoleBuilder) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	// The empty display name makes the SDK use each role's own display name.
	return []*v2.Entitlement{
		entitlement.NewAssignmentEntitlement(
			nil,
			clmRoleAssignedTag,
			entitlement.WithDisplayName(""),
			entitlement.WithDescription("Assigned this CLM role"),
		),
	}, nil, nil
}

func (b *clmRoleBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (b *clmRoleBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (b *clmRoleBuilder) GrantsForResourceType(ctx context.Context, _ string, attr rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	if !b.includeMembers {
		return nil, &rs.SyncOpResults{}, nil
	}

	page, found, err := session.GetJSON[clmMemberRolesPage](ctx, attr.Session, clmMemberRolesPageKey(attr.PageToken.Token), sessions.WithPrefix(clmMemberRolesSessionPrefix))
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: reading CLM member roles from the session store: %w", err)
	}

	var annos annotations.Annotations
	if !found {
		// A miss (a sync that didn't list this clm_member page) reads the page from CLM.
		var members []client.ClmMember
		members, page.NextPageToken, annos, err = b.client.ListMembers(ctx, client.PageOptions{
			PageSize:  attr.PageToken.Size,
			PageToken: attr.PageToken.Token,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("baton-docusign: listing CLM members to derive role grants: %w", err)
		}
		page.MemberRoles = clmMemberRoles(members)
	}

	grants := make([]*v2.Grant, 0, len(page.MemberRoles))
	for memberID, role := range page.MemberRoles {
		roleResource := &v2.Resource{Id: &v2.ResourceId{ResourceType: clmRoleResourceType.Id, Resource: role}}
		memberResourceID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: memberID}
		grants = append(grants, grant.NewGrant(roleResource, clmRoleAssignedTag, memberResourceID, grant.WithAnnotation(&v2.GrantImmutable{})))
	}

	return grants, &rs.SyncOpResults{Annotations: annos, NextPageToken: page.NextPageToken}, nil
}

func newClmRoleBuilder(c *client.Client, includeMembers, includeFolders bool) *clmRoleBuilder {
	return &clmRoleBuilder{
		client:         c,
		includeMembers: includeMembers,
		includeFolders: includeFolders,
	}
}

// clmRoleDisplayName splits a camel-case role value into words, keeping acronyms together ("CLMAdmin" -> "CLM Admin").
func clmRoleDisplayName(role string) string {
	runes := []rune(role)
	spaced := make([]rune, 0, len(runes))
	for i, current := range runes {
		if i > 0 && unicode.IsUpper(current) {
			previous := runes[i-1]
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || (unicode.IsUpper(previous) && nextIsLower) {
				spaced = append(spaced, ' ')
			}
		}
		spaced = append(spaced, current)
	}
	return string(spaced)
}

// clmMemberRolesPage is one CLM members page, as member ID to role, and the token of the next page.
type clmMemberRolesPage struct {
	MemberRoles   map[string]string `json:"member_roles"`
	NextPageToken string            `json:"next_page_token"`
}

// clmMemberRolesPageKey is the CLM page token; the first page has none.
func clmMemberRolesPageKey(pageToken string) string {
	if pageToken == "" {
		return "first"
	}
	return pageToken
}

// clmMemberRoles maps each member ID to its role, leaving out members without either.
func clmMemberRoles(members []client.ClmMember) map[string]string {
	memberRoles := make(map[string]string, len(members))
	for _, member := range members {
		memberID := clmIDFromHref(member.Href)
		if member.Role != "" && memberID != "" {
			memberRoles[memberID] = member.Role
		}
	}
	return memberRoles
}
