package client

import (
	"fmt"
	"time"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type PageOptions struct {
	PageSize  int
	PageToken string
}

type Page struct {
	ResultSetSize int `json:"resultSetSize,string"`
	TotalSetSize  int `json:"totalSetSize,string"`
	StartPosition int `json:"startPosition,string"`
	EndPosition   int `json:"endPosition,string"`
}

type ErrorResponse struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"message"`
}

func (e *ErrorResponse) Message() string {
	return fmt.Sprintf("message: %s, errorCode: %s", e.ErrorMessage, e.ErrorCode)
}

type pageToken struct {
	StartPosition int `json:"start_position"`
}

type UsersResponse struct {
	Page
	Users []User `json:"users"`
}

type User struct {
	UserId     string `json:"userId"`
	UserName   string `json:"userName"`
	Email      string `json:"email"`
	UserStatus string `json:"userStatus"`
	IsAdmin    string `json:"isAdmin"`
	Permission string `json:"permissionProfileName"`
}

type GroupsResponse struct {
	Page
	Groups []Group `json:"groups"`
}

type Group struct {
	GroupId    string `json:"groupId"`
	GroupName  string `json:"groupName"`
	GroupType  string `json:"groupType"`
	UsersCount string `json:"usersCount"`
}

type SigningGroupResponse struct {
	Page
	SigningGroups []SigningGroup `json:"groups"`
}

type SigningGroup struct {
	SigningGroupId string             `json:"signingGroupId"`
	GroupName      string             `json:"groupName"`
	GroupType      string             `json:"groupType"`
	GroupEmail     string             `json:"groupEmail"`
	Created        string             `json:"created"`
	CreatedBy      string             `json:"createdBy"`
	Modified       string             `json:"modified"`
	ModifiedBy     string             `json:"modifiedBy"`
	Users          []SigningGroupUser `json:"users"`
}

// SigningGroupUser represents a user member of a SigningGroup.
type SigningGroupUser struct {
	UserID   string `json:"userId,omitempty"`
	UserName string `json:"userName,omitempty"`
	Email    string `json:"email,omitempty"`
}

type UserDetail struct {
	UserID                string       `json:"userId"`
	UserName              string       `json:"userName"`
	Email                 string       `json:"email"`
	IsAdmin               string       `json:"isAdmin"`
	UserStatus            string       `json:"userStatus"`
	PermissionProfileName string       `json:"permissionProfileName"`
	PermissionProfileID   string       `json:"permissionProfileId"`
	UserSettings          UserSettings `json:"userSettings"`
	GroupList             []Group      `json:"groupList"`
}

type UserSettings struct {
	CanManageAccount          string             `json:"canManageAccount"`
	AccountManagementGranular AccountManagement  `json:"accountManagementGranular"`
	CanSendEnvelope           string             `json:"canSendEnvelope"`
	CanSignEnvelope           string             `json:"canSignEnvelope"`
	AllowSendOnBehalfOf       string             `json:"allowSendOnBehalfOf"`
	BulkSend                  string             `json:"bulkSend"`
	CanSendAPIRequests        string             `json:"canSendAPIRequests"`
	EnableSequentialSigningUI string             `json:"enableSequentialSigningUI"`
	EnableDSPro               string             `json:"enableDSPro"`
	CanUseScratchpad          string             `json:"canUseScratchpad"`
	CanCreateWorkspaces       string             `json:"canCreateWorkspaces"`
	EnableTransactionPoint    string             `json:"enableTransactionPoint"`
	PowerFormMode             string             `json:"powerFormMode"`
	APICanExportAC            string             `json:"apiCanExportAC"`
	EnableVaulting            string             `json:"enableVaulting"`
	CanManageTemplates        string             `json:"canManageTemplates"`
	CanEditSharedAddressbook  string             `json:"canEditSharedAddressbook"`
	AdminOnly                 string             `json:"adminOnly"`
	CanManageDistributor      string             `json:"canManageDistributor"`
	CanManageOrganization     string             `json:"canManageOrganization"`
	CanUseSmartContracts      string             `json:"canUseSmartContracts"`
	SignerEmailNotifications  EmailNotifications `json:"signerEmailNotifications"`
	SenderEmailNotifications  EmailNotifications `json:"senderEmailNotifications"`
}

type AccountManagement struct {
	CanManageUsers                   string `json:"canManageUsers"`
	CanManageAdmins                  string `json:"canManageAdmins"`
	CanManageAccountSettings         string `json:"canManageAccountSettings"`
	CanManageReporting               string `json:"canManageReporting"`
	CanManageAccountSecuritySettings string `json:"canManageAccountSecuritySettings"`
}

type EmailNotifications struct {
	EnvelopeActivation string `json:"envelopeActivation"`
	EnvelopeComplete   string `json:"envelopeComplete"`
	EnvelopeDeclined   string `json:"envelopeDeclined"`
}

type NewUser struct {
	UserName     string        `json:"userName"`
	Email        string        `json:"email"`
	UserSettings *UserSettings `json:"userSettings,omitempty"`
}

type CreateUsersRequest struct {
	NewUsers []NewUser `json:"newUsers"`
}

type UserCreationResponse struct {
	NewUsers []struct {
		UserId          string `json:"userId"`
		URI             string `json:"uri"`
		Email           string `json:"email"`
		UserName        string `json:"userName"`
		UserStatus      string `json:"userStatus"`
		CreatedDateTime string `json:"createdDateTime"`
		MembershipId    string `json:"membershipId"`
		ErrorDetails    *struct {
			ErrorCode string `json:"errorCode"`
			Message   string `json:"message"`
		} `json:"errorDetails,omitempty"`
	} `json:"newUsers"`
}

type PermissionProfilesResponse struct {
	Page
	PermissionProfiles []PermissionProfile `json:"permissionProfiles"`
}

type PermissionProfile struct {
	PermissionProfileId   string    `json:"permissionProfileId"`
	PermissionProfileName string    `json:"permissionProfileName"`
	ModifiedDateTime      time.Time `json:"modifiedDateTime"`
	ModifiedByUserName    string    `json:"modifiedByUsername"`
}

// DeleteUsersRequest represents a request to delete users from an account.
type DeleteUsersRequest struct {
	Users []UserIdentifier `json:"users"`
}

// UserIdentifier represents a user identifier for deletion operations.
type UserIdentifier struct {
	UserId string `json:"userId"`
}

// DeleteUsersResponse represents the response from a user deletion request.
type DeleteUsersResponse struct {
	Users []struct {
		UserId       string `json:"userId"`
		UserName     string `json:"userName"`
		Email        string `json:"email"`
		UserStatus   string `json:"userStatus"`
		Success      string `json:"success"`
		ErrorDetails *struct {
			ErrorCode string `json:"errorCode"`
			Message   string `json:"message"`
		} `json:"errorDetails,omitempty"`
	} `json:"users"`
}

// UpdateGroupUsersRequest represents a request to add users to a group.
type UpdateGroupUsersRequest struct {
	Users []GroupUserIdentifier `json:"users"`
}

// GroupUserIdentifier represents a user identifier for group operations.
type GroupUserIdentifier struct {
	UserId string `json:"userId"`
}

// UpdateGroupUsersResponse represents the response from updating group users.
type UpdateGroupUsersResponse struct {
	Users []struct {
		UserName   string `json:"userName"`
		UserId     string `json:"userId"`
		UserType   string `json:"userType"`
		UserStatus string `json:"userStatus"`
		URI        string `json:"uri"`
	} `json:"users"`
}

// UpdateSigningGroupRequest represents a request to update a signing group.
type UpdateSigningGroupRequest struct {
	Users []SigningGroupUserIdentifier `json:"users"`
}

// SigningGroupUserIdentifier represents a user identifier for signing group operations.
type SigningGroupUserIdentifier struct {
	UserName string `json:"userName"`
	Email    string `json:"email"`
}

// UpdateSigningGroupResponse represents the response from updating a signing group.
type UpdateSigningGroupResponse struct {
	SigningGroupId string `json:"signingGroupId"`
	GroupName      string `json:"groupName"`
	GroupType      string `json:"groupType"`
	Created        string `json:"created"`
	CreatedBy      string `json:"createdBy"`
	Modified       string `json:"modified"`
	ModifiedBy     string `json:"modifiedBy"`
	Users          []struct {
		UserName string `json:"userName"`
		Email    string `json:"email"`
	} `json:"users"`
}

// UpdateUserProfileRequest represents a request to update a user's permission profile.
type UpdateUserProfileRequest struct {
	UserDetails struct {
		PermissionProfileId string `json:"permissionProfileId"`
	} `json:"userDetails"`
}

// UserInfoResponse represents the response from DocuSign's OAuth User Info endpoint.
type UserInfoResponse struct {
	Sub      string        `json:"sub"`
	Name     string        `json:"name"`
	Email    string        `json:"email"`
	Accounts []AccountInfo `json:"accounts"`
}

// AccountInfo represents account information from the User Info response.
type AccountInfo struct {
	AccountId      string `json:"account_id"`
	IsDefault      bool   `json:"is_default"`
	AccountName    string `json:"account_name"`
	BaseURI        string `json:"base_uri"`
	OrganizationId string `json:"organization_id"`
}
