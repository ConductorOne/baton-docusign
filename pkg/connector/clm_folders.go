package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ connectorbuilder.StaticEntitlementSyncerV2 = (*clmFolderBuilder)(nil)

// The 5 grantable Baton entitlement slugs for CLM folder security, in ascending order
// of access.
const (
	clmFolderSlugView                    = "view"
	clmFolderSlugViewCreate              = "view_create"
	clmFolderSlugViewEdit                = "view_edit"
	clmFolderSlugViewEditDelete          = "view_edit_delete"
	clmFolderSlugViewEditDeleteSetAccess = "view_edit_delete_set_access"
)

// clmFolderEntitlement pairs a grantable Baton entitlement slug with the CLM
// AccessType it corresponds to. Only the 5 named, grantable tiers are exposed —
// InheritFromParentFolder (an absence-of-override marker, not a grant) and Custom (an
// arbitrary flag combination that can't be round-tripped to a single named tier)
// are deliberately excluded.
type clmFolderEntitlement struct {
	slug        string
	accessType  string
	displayName string
}

var clmFolderEntitlements = []clmFolderEntitlement{
	{clmFolderSlugView, client.ClmAccessTypeView, "View"},
	{clmFolderSlugViewCreate, client.ClmAccessTypeViewCreate, "View & Create"},
	{clmFolderSlugViewEdit, client.ClmAccessTypeViewEdit, "View & Edit"},
	{clmFolderSlugViewEditDelete, client.ClmAccessTypeViewEditDelete, "View, Edit, & Delete"},
	{clmFolderSlugViewEditDeleteSetAccess, client.ClmAccessTypeViewEditDeleteSetAccess, "View, Edit, Delete, & Set Access"},
}

// clmFolderBuilder implements resource listing and folder-security entitlements/grants
// for CLM Folders. Uses StaticEntitlementSyncerV2 since every folder shares the same 5
// grantable access tiers (see resource_types.go's SkipEntitlements annotation).
type clmFolderBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

func (f *clmFolderBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return clmFolderResourceType
}

// List discovers folders via Search — CLM's Folders object has no flat list-all
// endpoint, unlike Groups/Members/PermissionSets.
func (f *clmFolderBuilder) List(ctx context.Context, _ *v2.ResourceId, attr rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var resources []*v2.Resource

	bag, pageToken, err := parsePageToken(attr.PageToken.Token, &v2.ResourceId{ResourceType: clmFolderResourceType.Id})
	if err != nil {
		return nil, nil, err
	}

	folders, nextPageToken, annos, err := f.client.SearchFolders(ctx, client.PageOptions{
		PageSize:  attr.PageToken.Size,
		PageToken: pageToken,
	})
	if err != nil {
		if attr.PageToken.Token == "" && isOptInFeatureUnavailableError(err) {
			clmSkipLogLevel(ctx, err)("baton-docusign: CLM is not available for this account or token, skipping clm_folder sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, err
	}

	for _, folder := range folders {
		folderResource, err := parseIntoClmFolderResource(&folder)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, folderResource)
	}

	var outToken string
	if nextPageToken != "" {
		outToken, err = bag.NextToken(nextPageToken)
		if err != nil {
			return nil, nil, err
		}
	}

	return resources, &rs.SyncOpResults{
		Annotations:   annos,
		NextPageToken: outToken,
	}, nil
}

// Entitlements returns nil — the SDK does not call this when StaticEntitlementSyncerV2
// is implemented (see StaticEntitlements below).
func (f *clmFolderBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// StaticEntitlements declares the 5 grantable folder-security tiers, stamped by the
// SDK onto every synced clm_folder resource.
func (f *clmFolderBuilder) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	ents := make([]*v2.Entitlement, 0, len(clmFolderEntitlements))
	for _, fe := range clmFolderEntitlements {
		ents = append(ents, v2.Entitlement_builder{
			Slug:        fe.slug,
			DisplayName: fe.displayName,
			Description: fmt.Sprintf("%s access to the folder", fe.displayName),
			Purpose:     v2.Entitlement_PURPOSE_VALUE_PERMISSION,
			GrantableTo: []*v2.ResourceType{clmMemberResourceType, clmGroupResourceType, clmRoleResourceType},
		}.Build())
	}
	return ents, nil, nil
}

// Grants reads the folder's explicit (non-inherited) security entries across all
// three principal-type collections (Groups/Roles/Users — see ClmFolderSecurity's doc)
// and emits a grant per entry that maps cleanly to one of the 5 static tiers. Entries
// that don't (Custom, InheritFromParentFolder, or an unrecognized AccessType) are
// skipped, not approximated — see clmSlugForAccessType.
func (f *clmFolderBuilder) Grants(ctx context.Context, folderResource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	folder, annos, err := f.client.GetFolder(ctx, folderResource.Id.Resource, "Security")
	if err != nil {
		return nil, nil, fmt.Errorf("baton-docusign: getting security for CLM folder %s: %w", folderResource.Id.Resource, err)
	}

	var grants []*v2.Grant

	for _, entry := range folder.Security.Groups {
		slug, ok := clmSlugForAccessType(entry.AccessType)
		if !ok {
			if !clmIsBenignUnmappedAccessType(entry.AccessType) {
				ctxzap.Extract(ctx).Debug("baton-docusign: skipping CLM folder group-security entry with an unmapped AccessType",
					zap.String("folder_id", folderResource.Id.Resource), zap.String("group_href", entry.Href), zap.String("access_type", entry.AccessType))
			}
			continue
		}
		principalID := &v2.ResourceId{ResourceType: clmGroupResourceType.Id, Resource: clmIDFromHref(entry.Href)}
		// Per this project's grant-expansion convention: every grant whose principal
		// is a group must carry GrantExpandable so C1 also shows the group's
		// individual members as having access through it.
		grantOpts := []grant.GrantOption{grant.WithAnnotation(v2.GrantExpandable_builder{
			EntitlementIds: []string{fmt.Sprintf("%s:%s:%s", clmGroupResourceType.Id, principalID.Resource, entitlementClmGroupMember)},
		}.Build())}
		grants = append(grants, grant.NewGrant(folderResource, slug, principalID, grantOpts...))
	}

	for _, entry := range folder.Security.Roles {
		slug, ok := clmSlugForAccessType(entry.AccessType)
		if !ok {
			if !clmIsBenignUnmappedAccessType(entry.AccessType) {
				ctxzap.Extract(ctx).Debug("baton-docusign: skipping CLM folder role-security entry with an unmapped AccessType",
					zap.String("folder_id", folderResource.Id.Resource), zap.String("role", entry.Item), zap.String("access_type", entry.AccessType))
			}
			continue
		}
		if !clmIsKnownRole(entry.Item) {
			// clm_role is a fixed, hardcoded 5-role list (clmRoleBuilder.List) — a role
			// name outside that set has no synced principal to grant against. Skip
			// rather than emit a grant to a dangling/unsynced resource.
			ctxzap.Extract(ctx).Debug("baton-docusign: skipping CLM folder role-security entry for an unrecognized role",
				zap.String("folder_id", folderResource.Id.Resource), zap.String("role", entry.Item))
			continue
		}
		principalID := &v2.ResourceId{ResourceType: clmRoleResourceType.Id, Resource: entry.Item}
		grants = append(grants, grant.NewGrant(folderResource, slug, principalID))
	}

	for _, entry := range folder.Security.Users {
		slug, ok := clmSlugForAccessType(entry.AccessType)
		if !ok {
			if !clmIsBenignUnmappedAccessType(entry.AccessType) {
				ctxzap.Extract(ctx).Debug("baton-docusign: skipping CLM folder user-security entry with an unmapped AccessType",
					zap.String("folder_id", folderResource.Id.Resource), zap.String("member_href", entry.Href), zap.String("access_type", entry.AccessType))
			}
			continue
		}
		principalID := &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: clmIDFromHref(entry.Href)}
		grants = append(grants, grant.NewGrant(folderResource, slug, principalID))
	}

	return grants, &rs.SyncOpResults{Annotations: annos}, nil
}

// Grant sets a folder-security entry for the principal at the entitlement's tier.
// Read-before-write: fetches the folder's current complete security state, modifies
// only the one entry belonging to this principal (in whichever of Groups/Roles/Users
// it belongs to), and sends the complete state back — see clmFolderSecurityToWrite and
// PatchFolderSecurity's doc for why sending only the changed entry isn't safe. Uses
// GetFolderFresh, not GetFolder, since this must see the result of any write that just
// happened.
func (f *clmFolderBuilder) Grant(ctx context.Context, principal *v2.Resource, ent *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	accessType, ok := clmAccessTypeForSlug(ent.Slug)
	if !ok {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-docusign: unknown CLM folder entitlement slug %q", ent.Slug)
	}
	folderID := ent.Resource.Id.Resource

	// Same guard as Revoke, and for the same reason: the group/member branches below are
	// only incidentally covered by clmPreferredHref's own empty-id check, but the role
	// branch compares principal.Id.Resource to Item by exact string with nothing else
	// upstream to reject an empty value — this catches all three uniformly.
	if principal.Id.Resource == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-docusign: granting CLM folder security: principal missing native ID")
	}
	// folderID has the same theoretical gap: parseIntoClmFolderResource derives it via
	// clmIDFromHref(folder.Href) with no non-empty check, so an empty Href would produce
	// a folder resource with an empty ID. An empty folderID here would hit the
	// collection-root path (buildClmClientURL's "/v2/%s/folders/%s" with an empty last
	// segment) instead of failing clearly client-side.
	if folderID == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-docusign: granting CLM folder security: folder missing native ID")
	}

	folder, getAnnos, err := f.client.GetFolderFresh(ctx, folderID, "Security")
	if err != nil {
		return nil, getAnnos, fmt.Errorf("baton-docusign: getting security for CLM folder %s: %w", folderID, err)
	}
	write := clmFolderSecurityToWrite(folder.Security)

	switch principal.Id.ResourceType {
	case clmGroupResourceType.Id:
		// Prefer a real, server-issued Href already on hand over one derived from the
		// discovered CLM base URL — see clmPreferredHref's doc. The principal's own
		// profile href (parseIntoClmGroupResource still populates it for display) comes
		// first: it's this exact group's own recorded Href, not a sibling's to derive
		// from, so it's the most direct sample available when the resource happens to
		// carry one — only ever a sample, never required, so an identity-only principal
		// (no profile at all) still falls through to the other-entries/fallback path
		// unchanged.
		groupSampleHrefs := clmSampleHrefsFrom(principal, write.Groups, func(e client.ClmGroupSecurityEntry) string { return e.Href })
		groupHref, err := clmPreferredHref(ctx, principal.Id.Resource, groupSampleHrefs, func() (string, error) {
			return f.client.GroupHref(ctx, principal.Id.Resource)
		})
		if err != nil {
			return nil, getAnnos, fmt.Errorf("baton-docusign: resolving href for CLM group %s: %w", principal.Id.Resource, err)
		}
		if i := clmFindGroupSecurityIndex(write.Groups, groupHref); i >= 0 {
			if slug, ok := clmSlugForAccessType(write.Groups[i].AccessType); ok && slug == ent.Slug {
				return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
			}
			write.Groups[i].AccessType = accessType
		} else {
			write.Groups = append(write.Groups, client.ClmGroupSecurityEntry{AccessType: accessType, Href: groupHref})
		}
	case clmRoleResourceType.Id:
		roleName := principal.Id.Resource
		if i := clmFindRoleSecurityIndex(write.Roles, roleName); i >= 0 {
			if slug, ok := clmSlugForAccessType(write.Roles[i].AccessType); ok && slug == ent.Slug {
				return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
			}
			write.Roles[i].AccessType = accessType
		} else {
			write.Roles = append(write.Roles, client.ClmRoleSecurityEntry{AccessType: accessType, Item: roleName})
		}
	case clmMemberResourceType.Id:
		// Same rationale as the group case above.
		userSampleHrefs := clmSampleHrefsFrom(principal, write.Users, func(e client.ClmUserSecurityEntry) string { return e.Href })
		memberHref, err := clmPreferredHref(ctx, principal.Id.Resource, userSampleHrefs, func() (string, error) {
			return f.client.MemberHref(ctx, principal.Id.Resource)
		})
		if err != nil {
			return nil, getAnnos, fmt.Errorf("baton-docusign: resolving href for CLM member %s: %w", principal.Id.Resource, err)
		}
		if i := clmFindUserSecurityIndex(write.Users, memberHref); i >= 0 {
			if slug, ok := clmSlugForAccessType(write.Users[i].AccessType); ok && slug == ent.Slug {
				return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
			}
			write.Users[i].AccessType = accessType
		} else {
			write.Users = append(write.Users, client.ClmUserSecurityEntry{AccessType: accessType, Href: memberHref})
		}
	default:
		return nil, getAnnos, status.Errorf(codes.InvalidArgument, "baton-docusign: invalid principal type for CLM folder security: %s", principal.Id.ResourceType)
	}

	patchAnnos, err := f.client.PatchFolderSecurity(ctx, folderID, write)
	if err != nil {
		return nil, patchAnnos, fmt.Errorf("baton-docusign: granting CLM folder security: %w", err)
	}

	return nil, patchAnnos, nil
}

// clmFolderSecurityToWrite converts a folder's read-side security state (paginated
// per-collection, see ClmFolderSecurity) into the plain-list shape Grant/Revoke send
// back to PatchFolderSecurity, taking a defensive copy of each collection so callers
// can mutate the result without aliasing the original read.
func clmFolderSecurityToWrite(sec client.ClmFolderSecurity) client.ClmFolderSecurityWrite {
	groups := make([]client.ClmGroupSecurityEntry, len(sec.Groups))
	copy(groups, sec.Groups)
	roles := make([]client.ClmRoleSecurityEntry, len(sec.Roles))
	copy(roles, sec.Roles)
	users := make([]client.ClmUserSecurityEntry, len(sec.Users))
	copy(users, sec.Users)
	return client.ClmFolderSecurityWrite{Groups: groups, Roles: roles, Users: users}
}

// clmFindSecurityIndexByHref returns the index of the entry whose Href identifies
// targetHref (compared via clmIDFromHref, since the read-side Href shape isn't
// guaranteed to match exactly — see client.GroupHref/MemberHref), or -1 if not found.
// Shared by clmFindGroupSecurityIndex and clmFindUserSecurityIndex, which differ only in
// entry type.
func clmFindSecurityIndexByHref[T any](entries []T, hrefOf func(T) string, targetHref string) int {
	targetID := clmIDFromHref(targetHref)
	for i, e := range entries {
		if clmIDFromHref(hrefOf(e)) == targetID {
			return i
		}
	}
	return -1
}

func clmFindGroupSecurityIndex(entries []client.ClmGroupSecurityEntry, groupHref string) int {
	return clmFindSecurityIndexByHref(entries, func(e client.ClmGroupSecurityEntry) string { return e.Href }, groupHref)
}

// clmFindRoleSecurityIndex returns the index of the entry for roleName, or -1 if not
// found. Roles are compared by exact name, not clmIDFromHref — a role's Item is
// already the bare name, never a Href.
func clmFindRoleSecurityIndex(entries []client.ClmRoleSecurityEntry, roleName string) int {
	for i, e := range entries {
		if e.Item == roleName {
			return i
		}
	}
	return -1
}

func clmFindUserSecurityIndex(entries []client.ClmUserSecurityEntry, memberHref string) int {
	return clmFindSecurityIndexByHref(entries, func(e client.ClmUserSecurityEntry) string { return e.Href }, memberHref)
}

// Revoke sets the principal's folder-security entry to NoAccess (not removed — same
// read-modify-write pattern as Grant) — same endpoint as Grant, no Put round-trip
// needed.
func (f *clmFolderBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	folderID := grantObj.Entitlement.Resource.Id.Resource
	principal := grantObj.Principal

	// Guards all three branches below in one place: clmFindGroupSecurityIndex and
	// clmFindUserSecurityIndex reduce an empty ID to "" via clmIDFromHref, which would
	// match any security entry whose Href is empty or ends in a trailing slash; the role
	// branch compares principal.Id.Resource to Item by exact string, so an empty ID would
	// just as wrongly match an entry with an empty Item. Grant carries the identical
	// guard for the identical reason — keep the two in sync.
	if principal.Id.Resource == "" {
		return nil, status.Errorf(codes.InvalidArgument, "baton-docusign: revoking CLM folder security: principal missing native ID")
	}
	// Same reasoning as Grant's identical guard: an empty folderID would hit the CLM
	// API's folders collection root instead of failing clearly client-side.
	if folderID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "baton-docusign: revoking CLM folder security: folder missing native ID")
	}

	folder, getAnnos, err := f.client.GetFolderFresh(ctx, folderID, "Security")
	if err != nil {
		return getAnnos, fmt.Errorf("baton-docusign: getting security for CLM folder %s: %w", folderID, err)
	}
	write := clmFolderSecurityToWrite(folder.Security)

	switch principal.Id.ResourceType {
	case clmGroupResourceType.Id:
		// No need to build a real Href via client.GroupHref here: clmFindGroupSecurityIndex
		// only ever compares by clmIDFromHref, so principal.Id.Resource (already a bare ID)
		// works directly and this skips a needless ensureClmReady round trip.
		i := clmFindGroupSecurityIndex(write.Groups, principal.Id.Resource)
		if i < 0 || write.Groups[i].AccessType == client.ClmAccessTypeNoAccess {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		write.Groups[i].AccessType = client.ClmAccessTypeNoAccess
	case clmRoleResourceType.Id:
		roleName := principal.Id.Resource
		i := clmFindRoleSecurityIndex(write.Roles, roleName)
		if i < 0 || write.Roles[i].AccessType == client.ClmAccessTypeNoAccess {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		write.Roles[i].AccessType = client.ClmAccessTypeNoAccess
	case clmMemberResourceType.Id:
		// Same reasoning as the group case above: clmFindUserSecurityIndex only compares
		// by clmIDFromHref, so no need to build a real Href via client.MemberHref.
		i := clmFindUserSecurityIndex(write.Users, principal.Id.Resource)
		if i < 0 || write.Users[i].AccessType == client.ClmAccessTypeNoAccess {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		write.Users[i].AccessType = client.ClmAccessTypeNoAccess
	default:
		return getAnnos, status.Errorf(codes.InvalidArgument, "baton-docusign: invalid principal type for CLM folder security: %s", principal.Id.ResourceType)
	}

	patchAnnos, err := f.client.PatchFolderSecurity(ctx, folderID, write)
	if err != nil {
		return patchAnnos, fmt.Errorf("baton-docusign: revoking CLM folder security: %w", err)
	}

	return patchAnnos, nil
}

func newClmFolderBuilder(c *client.Client) *clmFolderBuilder {
	return &clmFolderBuilder{
		resourceType: clmFolderResourceType,
		client:       c,
	}
}

func parseIntoClmFolderResource(folder *client.ClmFolder) (*v2.Resource, error) {
	return rs.NewResource(
		folder.Name,
		clmFolderResourceType,
		clmIDFromHref(folder.Href),
	)
}

// clmAccessTypeForSlug maps a static entitlement slug back to its CLM AccessType.
func clmAccessTypeForSlug(slug string) (string, bool) {
	for _, fe := range clmFolderEntitlements {
		if fe.slug == slug {
			return fe.accessType, true
		}
	}
	return "", false
}

// clmSlugForAccessType determines which (if any) of the 5 static entitlement slugs an
// AccessType value corresponds to. Every folder-security entry (Group, Role, or User)
// carries AccessType directly — confirmed via DocuSign's own Folders.Patch reference
// page that there is no separate granular-boolean-flags representation to fall back
// to (an earlier version of this function assumed one; that assumption was wrong).
func clmSlugForAccessType(accessType string) (string, bool) {
	for _, fe := range clmFolderEntitlements {
		if fe.accessType == accessType {
			return fe.slug, true
		}
	}
	return "", false
}

// clmIsBenignUnmappedAccessType reports whether accessType is one of the three
// documented non-grantable values every folder-security entry can legitimately carry —
// NoAccess (this connector's own Revoke leaves entries in place at this value, so it
// appears on every subsequent sync of a revoked entry), Custom, and
// InheritFromParentFolder (an arbitrary flag combination or an absence-of-override
// marker, neither round-trippable to a single tier — see clmFolderEntitlement's doc).
// Grants() skips all three the same way, but only logs the ones NOT in this set, so a
// genuinely unrecognized AccessType doesn't get lost in three expected values large
// accounts can produce on every single sync.
func clmIsBenignUnmappedAccessType(accessType string) bool {
	switch accessType {
	case client.ClmAccessTypeNoAccess, client.ClmAccessTypeCustom, client.ClmAccessTypeInherit:
		return true
	default:
		return false
	}
}

// clmIsKnownRole reports whether name is one of the 5 fixed CLM account-level roles
// (client.ClmRoles) — the same fixed set clmRoleBuilder.List syncs as clm_role
// resources. Used to reject a folder-security Roles entry referencing a role outside
// that set before emitting a grant to it, since such a grant would target a principal
// this connector never syncs.
func clmIsKnownRole(name string) bool {
	for _, role := range client.ClmRoles {
		if role.Name == name {
			return true
		}
	}
	return false
}
