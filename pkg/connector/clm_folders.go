package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
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

// Grants reads the folder's explicit (non-inherited) security entries and emits a
// grant per entry that maps cleanly to one of the 5 static tiers. Entries that don't
// (Custom, InheritFromParentFolder, or an unrecognized flag combination) are skipped,
// not approximated — see clmAccessTypeForEntry.
func (f *clmFolderBuilder) Grants(ctx context.Context, folderResource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	folder, annos, err := f.client.GetFolder(ctx, folderResource.Id.Resource, "Security")
	if err != nil {
		return nil, nil, fmt.Errorf("getting security for CLM folder %s: %w", folderResource.Id.Resource, err)
	}

	var grants []*v2.Grant
	for _, entry := range folder.Security {
		slug, ok := clmSlugForEntry(entry)
		if !ok {
			// Custom / InheritFromParentFolder / unrecognized flags — not representable
			// as one of the 5 static tiers. Intentionally skipped, not approximated.
			continue
		}

		principalID, ok := clmPrincipalIDForItem(entry.Item)
		if !ok {
			// Item didn't match any known principal shape (clm_role name, or a
			// /groups/ or /members/ href) — skip rather than guess.
			continue
		}

		var grantOpts []grant.GrantOption
		if principalID.ResourceType == clmGroupResourceType.Id {
			// Per this project's grant-expansion convention: every grant whose
			// principal is a group must carry GrantExpandable so C1 also shows
			// the group's individual members as having access through it.
			grantOpts = append(grantOpts, grant.WithAnnotation(v2.GrantExpandable_builder{
				EntitlementIds: []string{fmt.Sprintf("%s:%s:%s", clmGroupResourceType.Id, principalID.Resource, entitlementClmGroupMember)},
			}.Build()))
		}

		grants = append(grants, grant.NewGrant(folderResource, slug, principalID, grantOpts...))
	}

	return grants, &rs.SyncOpResults{Annotations: annos}, nil
}

// Grant sets a folder-security entry for the principal at the entitlement's tier.
func (f *clmFolderBuilder) Grant(ctx context.Context, principal *v2.Resource, ent *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	accessType, ok := clmAccessTypeForSlug(ent.Slug)
	if !ok {
		return nil, nil, fmt.Errorf("baton-docusign: unknown CLM folder entitlement slug %q", ent.Slug)
	}

	folderID := ent.Resource.Id.Resource
	item, err := clmItemForPrincipal(principal)
	if err != nil {
		return nil, nil, err
	}

	// Read-before-write: check whether this principal already has this exact tier on
	// this folder before issuing a Patch, since Folders.Patch's merge-vs-replace
	// semantics for the Security field are undocumented. Uses GetFolderFresh, not
	// GetFolder, since this must see the result of any write that just happened.
	folder, getAnnos, err := f.client.GetFolderFresh(ctx, folderID, "Security")
	if err != nil {
		return nil, getAnnos, fmt.Errorf("getting security for CLM folder %s: %w", folderID, err)
	}
	// patchItem is what gets sent in the Patch call below. It defaults to the freshly
	// constructed item, but if an existing entry for this principal is found, it's
	// overwritten with that entry's own Item value (see the loop below for why).
	patchItem := item
	for _, entry := range folder.Security {
		// Compare by clmIDFromHref, not raw equality: entry.Item comes back from the
		// API's read side, which isn't guaranteed to match item's exact Href shape
		// (see clmPrincipalIDForItem's own normalization for the same reason) — a bare
		// ID and a Href ending in that ID must still be treated as the same principal.
		if clmIDFromHref(entry.Item) == clmIDFromHref(item) {
			if slug, ok := clmSlugForEntry(entry); ok && slug == ent.Slug {
				return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
			}
			// Patch this existing entry using its own Item value, not the freshly
			// constructed one: if the API's Patch upserts by exact Item match (as
			// opposed to whatever principal it logically references), sending our own
			// Href here could create a second entry alongside this one instead of
			// updating it, if the stored Item isn't in that same exact shape.
			patchItem = entry.Item
			break
		}
	}

	patchAnnos, err := f.client.PatchFolderSecurity(ctx, folderID, client.ClmSecurityEntry{
		AccessType: accessType,
		Item:       patchItem,
	})
	if err != nil {
		return nil, patchAnnos, fmt.Errorf("granting CLM folder security: %w", err)
	}

	return nil, patchAnnos, nil
}

// Revoke sets the principal's folder-security entry to NoAccess — same endpoint as
// Grant, no Put round-trip needed.
func (f *clmFolderBuilder) Revoke(ctx context.Context, grantObj *v2.Grant) (annotations.Annotations, error) {
	folderID := grantObj.Entitlement.Resource.Id.Resource
	item, err := clmItemForPrincipal(grantObj.Principal)
	if err != nil {
		return nil, err
	}

	folder, getAnnos, err := f.client.GetFolderFresh(ctx, folderID, "Security")
	if err != nil {
		return getAnnos, fmt.Errorf("getting security for CLM folder %s: %w", folderID, err)
	}

	found := false
	// patchItem defaults to the freshly constructed item but is overwritten below with
	// the matched entry's own Item value — see the identical pattern (and its
	// rationale) in Grant.
	patchItem := item
	for _, entry := range folder.Security {
		// See the matching comment in Grant: compare by clmIDFromHref, not raw
		// equality, since entry.Item's read-side format isn't guaranteed to match
		// item's Href shape exactly. Getting this wrong here is worse than in Grant —
		// a false "not found" makes Revoke report GrantAlreadyRevoked without ever
		// patching AccessType to NoAccess, silently leaving access in place.
		if clmIDFromHref(entry.Item) != clmIDFromHref(item) {
			continue
		}
		// Use the same tier resolution as Grant (clmSlugForEntry), not a raw AccessType
		// comparison — AccessType may be unpopulated on reads (see clmSlugForEntry's own
		// doc), in which case comparing it directly against NoAccess would treat an
		// already-NoAccess flag-only entry as still granted.
		if _, ok := clmSlugForEntry(entry); ok {
			found = true
			patchItem = entry.Item
			break
		}
	}
	if !found {
		return annotations.New(&v2.GrantAlreadyRevoked{}), nil
	}

	patchAnnos, err := f.client.PatchFolderSecurity(ctx, folderID, client.ClmSecurityEntry{
		AccessType: client.ClmAccessTypeNoAccess,
		Item:       patchItem,
	})
	if err != nil {
		return patchAnnos, fmt.Errorf("revoking CLM folder security: %w", err)
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

// clmSlugForEntry determines which (if any) of the 5 static entitlement slugs a
// folder-security entry corresponds to. Prefers the entry's AccessType directly when
// present; falls back to matching the entry's granular boolean flags against the known
// combination for each tier, since GET responses may return either representation.
func clmSlugForEntry(entry client.ClmSecurityEntry) (string, bool) {
	if entry.AccessType != "" {
		for _, fe := range clmFolderEntitlements {
			if fe.accessType == entry.AccessType {
				return fe.slug, true
			}
		}
		return "", false
	}

	flag := func(b *bool) bool { return b != nil && *b }
	create, move, read, see, setAccess, write := flag(entry.Create), flag(entry.Move), flag(entry.Read), flag(entry.See), flag(entry.SetAccess), flag(entry.Write)

	if !read || !see {
		return "", false
	}

	switch {
	case !create && !write && !move && !setAccess:
		return clmFolderSlugView, true
	case create && !write && !move && !setAccess:
		return clmFolderSlugViewCreate, true
	case create && write && !move && !setAccess:
		return clmFolderSlugViewEdit, true
	case create && write && move && !setAccess:
		return clmFolderSlugViewEditDelete, true
	case create && write && move && setAccess:
		return clmFolderSlugViewEditDeleteSetAccess, true
	default:
		return "", false
	}
}

// clmPrincipalIDForItem classifies a folder-security entry's Item reference into a
// principal ResourceId, using the same href-path heuristic as clmIDFromHref plus an
// exact match against the 5 known role names.
func clmPrincipalIDForItem(item string) (*v2.ResourceId, bool) {
	for _, role := range client.ClmRoles {
		if item == role.Name {
			return &v2.ResourceId{ResourceType: clmRoleResourceType.Id, Resource: role.Name}, true
		}
	}
	switch {
	case strings.Contains(item, "/groups/"):
		return &v2.ResourceId{ResourceType: clmGroupResourceType.Id, Resource: clmIDFromHref(item)}, true
	case strings.Contains(item, "/members/"):
		return &v2.ResourceId{ResourceType: clmMemberResourceType.Id, Resource: clmIDFromHref(item)}, true
	default:
		return nil, false
	}
}

// clmItemForPrincipal builds the Item reference to send when granting/revoking folder
// security to/from a principal — the inverse of clmPrincipalIDForItem.
func clmItemForPrincipal(principal *v2.Resource) (string, error) {
	switch principal.Id.ResourceType {
	case clmRoleResourceType.Id:
		return principal.Id.Resource, nil
	case clmGroupResourceType.Id:
		return clmGroupHrefFromResource(principal)
	case clmMemberResourceType.Id:
		trait, err := rs.GetUserTrait(principal)
		if err != nil {
			return "", fmt.Errorf("baton-docusign: failed to read CLM member trait: %w", err)
		}
		href := trait.GetProfile().GetFields()["href"].GetStringValue()
		if href == "" {
			return "", fmt.Errorf("baton-docusign: CLM member resource %s is missing its href profile field", principal.Id.Resource)
		}
		return href, nil
	default:
		return "", fmt.Errorf("baton-docusign: invalid principal type for CLM folder security: %s", principal.Id.ResourceType)
	}
}
