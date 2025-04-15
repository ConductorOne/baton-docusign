package client

type Page struct {
	ResultSetSize int `json:"resultSetSize,string"`
	TotalSetSize  int `json:"totalSetSize,string"`
	StartPosition int `json:"startPosition,string"`
	EndPosition   int `json:"endPosition,string"`
}

type User struct {
	UserId     string `json:"userId"`
	UserName   string `json:"userName"`
	Email      string `json:"email"`
	UserStatus string `json:"userStatus"`
	IsAdmin    string `json:"isAdmin"`
	Permission string `json:"permissionProfileName"`
}

type UsersResponse struct {
	Users []User `json:"users"`
	Page  Page
}

type Group struct {
	GroupId    string `json:"groupId"`
	GroupName  string `json:"groupName"`
	GroupType  string `json:"groupType"`
	UsersCount string `json:"usersCount"`
}

type GroupsResponse struct {
	Groups []Group `json:"groups"`
	Page   Page
}

// UserDetail representa la respuesta detallada del endpoint de usuario
type UserDetail struct {
	UserID                string       `json:"userId"`
	UserName              string       `json:"userName"`
	Email                 string       `json:"email"`
	IsAdmin               string       `json:"isAdmin"`
	UserStatus            string       `json:"userStatus"`
	PermissionProfileName string       `json:"permissionProfileName"`
	UserSettings          UserSettings `json:"userSettings"`
	GroupList             []Group      `json:"groupList"`
}

// UserSettings contiene todos los permisos granularies del usuario
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

// AccountManagement contiene permisos granulares de gestión de cuenta
type AccountManagement struct {
	CanManageUsers                   string `json:"canManageUsers"`
	CanManageAdmins                  string `json:"canManageAdmins"`
	CanManageAccountSettings         string `json:"canManageAccountSettings"`
	CanManageReporting               string `json:"canManageReporting"`
	CanManageAccountSecuritySettings string `json:"canManageAccountSecuritySettings"`
}

// EmailNotifications contiene configuraciones de notificaciones
type EmailNotifications struct {
	EnvelopeActivation string `json:"envelopeActivation"`
	EnvelopeComplete   string `json:"envelopeComplete"`
	EnvelopeDeclined   string `json:"envelopeDeclined"`
}
