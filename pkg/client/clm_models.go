package client

import (
	"encoding/json"
	"fmt"
)

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

// ClmErrorResponse is CLM's error envelope — confirmed via a live 401 from a real CLM
// tenant: {"Error":{"HttpStatusCode":401,"UserMessage":"Access Denied",
// "DeveloperMessage":"Access Denied","ErrorCode":103,"ReferenceId":"..."}}. Errors are
// nested under "Error", not a top-level "Message" field.
type ClmErrorResponse struct {
	Error struct {
		UserMessage      string `json:"UserMessage"`
		DeveloperMessage string `json:"DeveloperMessage"`
		ErrorCode        int    `json:"ErrorCode"`
		ReferenceId      string `json:"ReferenceId"`
	} `json:"Error"`
}

func (e *ClmErrorResponse) Message() string {
	primary := e.Error.UserMessage
	if primary == "" {
		primary = e.Error.DeveloperMessage
	}
	if primary == "" {
		return "unknown CLM API error"
	}
	if e.Error.UserMessage != "" && e.Error.DeveloperMessage != "" && e.Error.DeveloperMessage != e.Error.UserMessage {
		return fmt.Sprintf("CLM API error %d: %s (%s)", e.Error.ErrorCode, primary, e.Error.DeveloperMessage)
	}
	return fmt.Sprintf("CLM API error %d: %s", e.Error.ErrorCode, primary)
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

// ClmFolderSearchTaskResponse is CLM's FolderSearchTasks response envelope — the same
// shape whether returned by the initial POST (create) or a poll GET on the task's own
// Href. Confirmed live for the POST/"Success" case (Result already populated with the
// same Items/Offset/Limit/Total/Next fields as every other CLM list endpoint); the CLM
// Task API 101 docs describe the identical {Status, Href, Result} envelope for
// DocumentSearchTasks, CLM's sibling search-task resource — see SearchFolders' doc in
// clm_client.go for what's confirmed vs. assumed.
type ClmFolderSearchTaskResponse struct {
	Status string         `json:"Status"`
	Href   string         `json:"Href"`
	Result *ClmFolderPage `json:"Result,omitempty"`
}

// ClmChangeSecurityTaskResponse is CLM's ChangeSecurityTasks response envelope — per
// the CLM API Reference's documented "Task.ChangeSecurityTask" schema (confirmed live
// via the rendered doc site, not just its collapsed default view — see
// ClmChangeSecurityTaskRequest's doc for why that distinction mattered here). The full
// schema also lists top-level Folder and Security fields alongside Href/Status, but
// PatchFolderSecurity only needs Status to decide success/failure; Href is this task's
// own poll URL, echoed back from the create call. Status uses a distinct, lowercase
// vocabulary from FolderSearchTasks — see PatchFolderSecurity's doc in clm_client.go.
// Not independently confirmed live (no more live-testing against the customer tenant).
type ClmChangeSecurityTaskResponse struct {
	Href   string `json:"Href"`
	Status string `json:"Status"`
}

// ClmFolderSecurity is a folder's explicit (non-inherited) security assignments.
// Confirmed live against a real CLM tenant (GetFolder?expand=Security) to be three
// SEPARATE, flat (non-paginated) arrays by principal type — no First/Href/Last/Limit/
// Next/Offset/Previous/Total pagination envelope wraps them the way every other CLM
// list response does; an earlier version of this struct wrapped each in a page type
// based on an unconfirmed reading of the Folders.Patch reference page, which caused a
// live sync to fail entirely with a JSON unmarshal error (array where an object with an
// Items field was expected).
type ClmFolderSecurity struct {
	Groups []ClmGroupSecurityEntry `json:"Groups,omitempty"`
	Roles  []ClmRoleSecurityEntry  `json:"Roles,omitempty"`
	Users  []ClmUserSecurityEntry  `json:"Users,omitempty"`
}

// ClmGroupSecurityEntry is one folder-security grant to a CLM Group. Confirmed live:
// the wire shape nests the group's own fields under an "Item" key, sibling to
// AccessType — {"Item": {"Href":...,"Name":...,...}, "AccessType":"View"} — not a flat
// merge of AccessType into the group's fields as an earlier version of this struct
// assumed (that assumption was never actually confirmed against a live tenant, despite
// its doc comment's claim otherwise). Kept as a flat Go struct via custom (Un)MarshalJSON
// so callers in pkg/connector/clm_folders.go don't need to know about the wire-level
// nesting — mirrors ClmRoleSecurityEntry's existing bare-Item shape, just for an object
// Item instead of a string one.
type ClmGroupSecurityEntry struct {
	AccessType  string
	Href        string
	Name        string
	GroupType   string
	Description string
	CreatedDate string
	UpdatedDate string
}

type clmGroupSecurityItem struct {
	Href        string `json:"Href"`
	Name        string `json:"Name,omitempty"`
	GroupType   string `json:"GroupType,omitempty"`
	Description string `json:"Description,omitempty"`
	CreatedDate string `json:"CreatedDate,omitempty"`
	UpdatedDate string `json:"UpdatedDate,omitempty"`
}

func (e ClmGroupSecurityEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Item       clmGroupSecurityItem `json:"Item"`
		AccessType string               `json:"AccessType,omitempty"`
	}{
		Item: clmGroupSecurityItem{
			Href:        e.Href,
			Name:        e.Name,
			GroupType:   e.GroupType,
			Description: e.Description,
			CreatedDate: e.CreatedDate,
			UpdatedDate: e.UpdatedDate,
		},
		AccessType: e.AccessType,
	})
}

func (e *ClmGroupSecurityEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		Item       clmGroupSecurityItem `json:"Item"`
		AccessType string               `json:"AccessType"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = ClmGroupSecurityEntry{
		AccessType:  wire.AccessType,
		Href:        wire.Item.Href,
		Name:        wire.Item.Name,
		GroupType:   wire.Item.GroupType,
		Description: wire.Item.Description,
		CreatedDate: wire.Item.CreatedDate,
		UpdatedDate: wire.Item.UpdatedDate,
	}
	return nil
}

// ClmRoleSecurityEntry is one folder-security grant to a CLM Role. Confirmed shape on
// writes (this connector's own PATCH body): flat {AccessType, Item}, Item the bare role
// name — unlike Groups/Users, a Role has no separate object to expand. Reads use a
// custom UnmarshalJSON tolerating either that flat string or a Groups/Users-style
// nested {Item: {Name: ...}} object: only Groups has been independently confirmed live
// (see ClmUserSecurityEntry's doc for the same gap on Users), so if CLM nests Roles too,
// decoding a JSON object into a bare Go string would otherwise hard-fail every
// clm_folder read/Grant/Revoke on that folder instead of just this one entry.
type ClmRoleSecurityEntry struct {
	AccessType string `json:"AccessType,omitempty"`
	Item       string `json:"Item"`
}

func (e ClmRoleSecurityEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AccessType string `json:"AccessType,omitempty"`
		Item       string `json:"Item"`
	}{AccessType: e.AccessType, Item: e.Item})
}

func (e *ClmRoleSecurityEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		AccessType string          `json:"AccessType"`
		Item       json.RawMessage `json:"Item"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var name string
	if len(wire.Item) > 0 {
		if err := json.Unmarshal(wire.Item, &name); err != nil {
			var obj struct {
				Name string `json:"Name"`
			}
			if err2 := json.Unmarshal(wire.Item, &obj); err2 != nil {
				return fmt.Errorf("baton-docusign: CLM role security entry Item is neither a string nor an object with Name: %w", err)
			}
			name = obj.Name
		}
	}
	*e = ClmRoleSecurityEntry{AccessType: wire.AccessType, Item: name}
	return nil
}

// ClmUserSecurityEntry is one folder-security grant to a CLM Member (user). Read via a
// custom UnmarshalJSON tolerating both ClmGroupSecurityEntry's confirmed nested
// {Item: {...}, AccessType} wire shape and a flat {Href, AccessType, ...} one: Users'
// shape isn't independently confirmed live (every populated folder-security entry found
// on the live tenant this was tested against was a Group), and guessing wrong on a
// struct-typed Item field fails silently (a missing key leaves Item's fields at their
// zero value, not a decode error) rather than loudly — see the review finding that
// caught this: every existing user's Href would decode as "", and since
// clmFolderSecurityToWrite round-trips the complete security state on every
// Grant/Revoke, an unrelated write could silently blank and then drop every other
// user's folder access. Deliberately doesn't repeat every field ClmMember has
// (Address*, City, Company, etc.): Grant/Revoke only ever need Href to identify the
// member, never reconstruct a full member profile from a security entry.
type ClmUserSecurityEntry struct {
	AccessType string
	Href       string
	Email      string
	UserName   string
	FirstName  string
	LastName   string
	Role       string
}

type clmUserSecurityItem struct {
	Href      string `json:"Href"`
	Email     string `json:"Email,omitempty"`
	UserName  string `json:"UserName,omitempty"`
	FirstName string `json:"FirstName,omitempty"`
	LastName  string `json:"LastName,omitempty"`
	Role      string `json:"Role,omitempty"`
}

func (e ClmUserSecurityEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Item       clmUserSecurityItem `json:"Item"`
		AccessType string              `json:"AccessType,omitempty"`
	}{
		Item: clmUserSecurityItem{
			Href:      e.Href,
			Email:     e.Email,
			UserName:  e.UserName,
			FirstName: e.FirstName,
			LastName:  e.LastName,
			Role:      e.Role,
		},
		AccessType: e.AccessType,
	})
}

func (e *ClmUserSecurityEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		Item       json.RawMessage `json:"Item"`
		AccessType string          `json:"AccessType"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var item clmUserSecurityItem
	if len(wire.Item) > 0 {
		if err := json.Unmarshal(wire.Item, &item); err != nil {
			return err
		}
	} else {
		// No "Item" key at all — the flat-shape fallback (see this type's doc). Decode
		// the same fields straight off the top level instead of leaving item at its zero
		// value, which would otherwise silently produce Href == "".
		if err := json.Unmarshal(data, &item); err != nil {
			return err
		}
	}
	*e = ClmUserSecurityEntry{
		AccessType: wire.AccessType,
		Href:       item.Href,
		Email:      item.Email,
		UserName:   item.UserName,
		FirstName:  item.FirstName,
		LastName:   item.LastName,
		Role:       item.Role,
	}
	return nil
}

// ClmChangeSecurityTaskRequest is the request body for POST .../changesecuritytasks —
// see PatchFolderSecurity's doc in clm_client.go. Confirmed via the CLM API Reference's
// interactive schema browser (its default collapsed view initially looked like a flat
// {Href, Security} pair sibling to Status — an earlier version of this struct assumed
// exactly that — but expanding "Folder" shows it's the *complete* Folder object schema,
// itself carrying its own nested Href and Security fields; the outer Href/Security
// alongside Status are generic Task-wrapper fields this doc-generation tool reuses
// across every Task type, not what ChangeSecurityTasks actually reads for a folder
// security change). The target folder and its new security both nest under Folder.
type ClmChangeSecurityTaskRequest struct {
	Folder ClmChangeSecurityTaskFolder `json:"Folder"`
}

// ClmChangeSecurityTaskFolder is the minimal Folder reference ChangeSecurityTasks'
// POST needs: which folder (Href) and what to set its security to (Security) — see
// ClmChangeSecurityTaskRequest's doc. The real Folder object has many more fields
// (Name, ParentFolder, Path, etc.); none are required here, mirroring how ParentFolder
// references elsewhere in this API only ever need Href.
type ClmChangeSecurityTaskFolder struct {
	Href     string                 `json:"Href"`
	Security ClmFolderSecurityWrite `json:"Security"`
}

// ClmFolderSecurityWrite is the plain-list (non-paginated) shape of ClmFolderSecurity
// used on writes — a write payload has no First/Href/Last/Limit/Next/Offset/Previous/
// Total metadata to send, mirroring ClmGroupList's precedent for the same reason on
// member-groups writes. Grant/Revoke always populate all three fields with the
// folder's complete current security (see clm_folders.go's clmFolderSecurityToWrite),
// not just the one changed entry: Folders.Patch's merge-vs-replace semantics for
// Security are undocumented, and sending the complete state is correct either way.
//
// Entries serialize via ClmGroupSecurityEntry/ClmUserSecurityEntry's MarshalJSON, so a
// PATCH sends the same {Item: {...}, AccessType} shape confirmed live on reads. This
// WAS tested live, against a disposable folder created and deleted solely for the
// check — and had no effect: see clm_client.go's package doc "Folders" section for the
// full evidence chain. Neither this shape nor a flat {Href, AccessType} one worked,
// with either PATCH or PUT; a distinct CLM error code ("136 - Missing Change Security
// Task") suggests the real mechanism is a dedicated Task API endpoint, not this generic
// object Patch. Grant/Revoke on clm_folder are NOT confirmed to work against a real CLM
// tenant — this is a known, open gap, not a residual unconfirmed assumption.
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
