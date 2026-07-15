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
	Security []ClmSecurityEntry `json:"Security,omitempty"`
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

// ClmSecurityEntry represents one folder-security grant: an access level for a single
// Role, Group, or User. AccessType is the named enum used for WRITES
// (InheritFromParentFolder/NoAccess/View/ViewCreate/ViewEdit/ViewEditDelete/
// ViewEditDeleteSetAccess/Custom) — reads via ?expand=Security instead return the
// granular boolean flags below; both represent the same underlying permission via two
// different shapes.
type ClmSecurityEntry struct {
	AccessType string `json:"AccessType"`
	// Item identifies the grantee — a Role, Group, or User depending on security
	// type. The reference format (Href vs raw ID) is handled defensively in
	// clmPrincipalIDForItem/clmItemForPrincipal (see pkg/connector/clm_folders.go).
	Item string `json:"Item"`

	// The following flags are only present on reads (not sent on writes) and only
	// when the read-side representation is used instead of AccessType.
	Create    *bool `json:"Create,omitempty"`
	Move      *bool `json:"Move,omitempty"`
	Read      *bool `json:"Read,omitempty"`
	See       *bool `json:"See,omitempty"`
	SetAccess *bool `json:"SetAccess,omitempty"`
	Write     *bool `json:"Write,omitempty"`
}

// ClmFolderSecurityPatch is the request body for PATCH .../folders/{id} when updating
// folder security.
type ClmFolderSecurityPatch struct {
	Security []ClmSecurityEntry `json:"Security"`
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
