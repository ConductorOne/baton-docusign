package client

import "fmt"

// ClmPage is the pagination metadata CLM's Object API embeds in every list response —
// distinct from eSignature's Page (see models.go). Each *Page wrapper type below embeds
// this plus its own Items field, matching the confirmed response shape:
// {First, Href, Items, Last, Limit, Next, Offset, Previous, Total}.
type ClmPage struct {
	First    string `json:"First"`
	Href     string `json:"Href"`
	Last     string `json:"Last"`
	Limit    int    `json:"Limit"`
	Next     string `json:"Next"`
	Offset   int    `json:"Offset"`
	Previous string `json:"Previous"`
	Total    int    `json:"Total"`
}

// ClmErrorResponse is CLM's error envelope. DocuSign's API reference does not document
// an error response shape, so this is intentionally loose; callers should log the raw
// body if fields don't populate as expected.
type ClmErrorResponse struct {
	Msg string `json:"Message"`
}

func (e *ClmErrorResponse) Message() string {
	if e.Msg == "" {
		return "unknown CLM API error"
	}
	return fmt.Sprintf("CLM API error: %s", e.Msg)
}

// ClmFolder represents a CLM Folder object.
type ClmFolder struct {
	Href                               string        `json:"Href"`
	Name                               string        `json:"Name"`
	Description                        string        `json:"Description"`
	Path                               string        `json:"Path"`
	CreatedBy                          string        `json:"CreatedBy"`
	CreatedDate                        string        `json:"CreatedDate"`
	UpdatedBy                          string        `json:"UpdatedBy"`
	UpdatedDate                        string        `json:"UpdatedDate"`
	ParentFolder                       *ClmFolderRef `json:"ParentFolder,omitempty"`
	PropagateAttributeGroupsToChildren bool          `json:"PropagateAttributeGroupsToChildren,omitempty"`
	// Security is only populated when the request used ?expand=Security, and only
	// reflects explicit assignments — not permissions inherited from a parent folder.
	Security ClmFolderSecurity `json:"Security,omitempty"`
}

// ClmFolderRef is a lightweight folder reference (e.g. ParentFolder).
type ClmFolderRef struct {
	Href string `json:"Href"`
	Name string `json:"Name,omitempty"`
}

// ClmFolderPage is the paginated collection of ClmFolder (e.g. from Search).
type ClmFolderPage struct {
	ClmPage
	Items []ClmFolder `json:"Items"`
}

// ClmFolderSecurity is a folder's explicit (non-inherited) security assignments,
// confirmed via DocuSign's own Folders.Patch reference page (pasted live, since the
// site is JS-rendered and unreachable by automated tools) to be three SEPARATE
// collections by principal type — not a single flat list, and not the
// AccessType-vs-boolean-flags dual representation an earlier version of this file
// assumed. No boolean flags (Create/Move/Read/See/SetAccess/Write) appear anywhere in
// the confirmed schema; every entry across all three collections carries AccessType
// directly.
type ClmFolderSecurity struct {
	Groups ClmGroupSecurityPage `json:"Groups,omitempty"`
	Roles  ClmRoleSecurityPage  `json:"Roles,omitempty"`
	Users  ClmUserSecurityPage  `json:"Users,omitempty"`
}

// ClmGroupSecurityEntry is one folder-security grant to a CLM Group. Confirmed shape:
// the full Group object's own fields (Href/Name/GroupType/Description/CreatedDate/
// UpdatedDate) plus AccessType — not a lean {Item, AccessType} pair.
type ClmGroupSecurityEntry struct {
	AccessType  string `json:"AccessType,omitempty"`
	Href        string `json:"Href"`
	Name        string `json:"Name,omitempty"`
	GroupType   string `json:"GroupType,omitempty"`
	Description string `json:"Description,omitempty"`
	CreatedDate string `json:"CreatedDate,omitempty"`
	UpdatedDate string `json:"UpdatedDate,omitempty"`
}

// ClmGroupSecurityPage is the paginated collection of ClmGroupSecurityEntry returned
// on a read (GetFolder?expand=Security). See ClmFolderSecurityWrite for the plain-list
// shape used on writes.
type ClmGroupSecurityPage struct {
	ClmPage
	Items []ClmGroupSecurityEntry `json:"Items"`
}

// ClmRoleSecurityEntry is one folder-security grant to a CLM Role. Confirmed shape:
// flat {AccessType, Item} — unlike Groups/Users, a Role has no separate object to
// expand, so Item is just the role name string.
type ClmRoleSecurityEntry struct {
	AccessType string `json:"AccessType,omitempty"`
	Item       string `json:"Item"`
}

// ClmRoleSecurityPage is the paginated collection of ClmRoleSecurityEntry.
type ClmRoleSecurityPage struct {
	ClmPage
	Items []ClmRoleSecurityEntry `json:"Items"`
}

// ClmUserSecurityEntry is one folder-security grant to a CLM Member (user). Confirmed
// shape: the Member object's own identifying fields plus AccessType — mirrors
// ClmGroupSecurityEntry's pattern. Deliberately doesn't repeat every field ClmMember
// has (Address*, City, Company, etc.): Grant/Revoke only ever need Href to identify
// the member, never reconstruct a full member profile from a security entry.
type ClmUserSecurityEntry struct {
	AccessType string `json:"AccessType,omitempty"`
	Href       string `json:"Href"`
	Email      string `json:"Email,omitempty"`
	UserName   string `json:"UserName,omitempty"`
	FirstName  string `json:"FirstName,omitempty"`
	LastName   string `json:"LastName,omitempty"`
	Role       string `json:"Role,omitempty"`
}

// ClmUserSecurityPage is the paginated collection of ClmUserSecurityEntry.
type ClmUserSecurityPage struct {
	ClmPage
	Items []ClmUserSecurityEntry `json:"Items"`
}

// ClmFolderSecurityPatch is the request body for PATCH .../folders/{id} when updating
// folder security.
type ClmFolderSecurityPatch struct {
	Security ClmFolderSecurityWrite `json:"Security"`
}

// ClmFolderSecurityWrite is the plain-list (non-paginated) shape of ClmFolderSecurity
// used on writes — a write payload has no First/Href/Last/Limit/Next/Offset/Previous/
// Total metadata to send, mirroring ClmGroupList's precedent for the same reason on
// member-groups writes. Grant/Revoke always populate all three fields with the
// folder's complete current security (see clm_folders.go's clmFolderSecurityToWrite),
// not just the one changed entry: Folders.Patch's merge-vs-replace semantics for
// Security are undocumented, and sending the complete state is correct either way.
type ClmFolderSecurityWrite struct {
	Groups []ClmGroupSecurityEntry `json:"Groups,omitempty"`
	Roles  []ClmRoleSecurityEntry  `json:"Roles,omitempty"`
	Users  []ClmUserSecurityEntry  `json:"Users,omitempty"`
}

// ClmAccessType enumerates the confirmed folder-security write values. Custom and
// InheritFromParentFolder are deliberately not exposed as grantable entitlements — see
// pkg/connector/clm_folders.go.
const (
	ClmAccessTypeInherit                 = "InheritFromParentFolder"
	ClmAccessTypeNoAccess                = "NoAccess"
	ClmAccessTypeView                    = "View"
	ClmAccessTypeViewCreate              = "ViewCreate"
	ClmAccessTypeViewEdit                = "ViewEdit"
	ClmAccessTypeViewEditDelete          = "ViewEditDelete"
	ClmAccessTypeViewEditDeleteSetAccess = "ViewEditDeleteSetAccess"
	ClmAccessTypeCustom                  = "Custom"
)

// ClmGroup represents a CLM Group object — distinct from eSignature's Group.
type ClmGroup struct {
	Href        string `json:"Href"`
	Name        string `json:"Name"`
	Description string `json:"Description,omitempty"`
	// GroupType is "security" or "distribution" per the confirmed schema.
	GroupType   string `json:"GroupType,omitempty"`
	CreatedDate string `json:"CreatedDate,omitempty"`
	UpdatedDate string `json:"UpdatedDate,omitempty"`
}

// ClmGroupPage is the paginated collection of ClmGroup, returned by read endpoints
// (GetAllGroups, a member's current groups, etc.). See ClmGroupList for the separate,
// leaner type used on write payloads.
type ClmGroupPage struct {
	ClmPage
	Items []ClmGroup `json:"Items"`
}

// ClmMemberGroupsPatch is the request body for PATCH/PUT .../members/{id} when
// granting (Patch, additive) or revoking (Put, full-replace) group membership.
// Deliberately a plain Items list, not the paginated ClmGroupPage used for reads — a
// write payload has no First/Href/Last/Limit/Next/Offset/Previous/Total metadata to
// send, and including response-only fields risks rejection if CLM's model binding is strict.
type ClmMemberGroupsPatch struct {
	Groups ClmGroupList `json:"Groups"`
}

// ClmGroupList is a bare list of groups, used for write payloads (see ClmMemberGroupsPatch).
type ClmGroupList struct {
	Items []ClmGroup `json:"Items"`
}

// ClmMember represents a CLM Member object — CLM's principal. Confirmed schema (richer
// than what the Patch/Put body alone shows, per Groups.GetUsers).
type ClmMember struct {
	Href       string `json:"Href"`
	Email      string `json:"Email"`
	UserName   string `json:"UserName"`
	FirstName  string `json:"FirstName"`
	LastName   string `json:"LastName"`
	MiddleName string `json:"MiddleName,omitempty"`
	// Role is an account-level enum: Guest/LimitedSubscriber/FullSubscriber/
	// UserAdministrator/SuperAdministrator. This is the "Role" grantee type
	// referenced in folder Security's "by role, group, and user".
	Role               string `json:"Role"`
	Company            string `json:"Company,omitempty"`
	Department         string `json:"Department,omitempty"`
	Title              string `json:"Title,omitempty"`
	CreatedDate        string `json:"CreatedDate,omitempty"`
	UpdatedDate        string `json:"UpdatedDate,omitempty"`
	ExemptFromUserSync bool   `json:"ExemptFromUserSync,omitempty"`
	PortalOnly         bool   `json:"PortalOnly,omitempty"`
}

// ClmMemberPage is the paginated collection of ClmMember.
type ClmMemberPage struct {
	ClmPage
	Items []ClmMember `json:"Items"`
}

// ClmPermissionSet represents a CLM PermissionSet object. Confirmed schema: Name +
// Permissions (a list of PrivilegeType strings) — an account/feature-privilege bundle,
// NOT the same thing as a folder's AccessType/boolean-flag access levels. Confirmed
// read-only: no assignment/grant endpoint exists anywhere in the CLM API.
type ClmPermissionSet struct {
	Href        string   `json:"Href"`
	Name        string   `json:"Name"`
	Permissions []string `json:"Permissions"`
}

// ClmPermissionSetPage is the paginated collection of ClmPermissionSet.
type ClmPermissionSetPage struct {
	ClmPage
	Items []ClmPermissionSet `json:"Items"`
}

// ClmRole is a static, hardcoded representation of one of the 5 fixed account-level
// Member.Role values. Not backed by an API call — needed so folder-security entries
// granted "by role" (Item referencing a Role rather than a Group/User) have a real
// synced resource to attach the grant to. See pkg/connector/clm_roles.go.
type ClmRole struct {
	Name string
}

// ClmRoles are the confirmed, fixed set of CLM account-level roles.
var ClmRoles = []ClmRole{
	{Name: "Guest"},
	{Name: "LimitedSubscriber"},
	{Name: "FullSubscriber"},
	{Name: "UserAdministrator"},
	{Name: "SuperAdministrator"},
}
