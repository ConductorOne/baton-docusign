package connector

// permissionDefinition defines the structure of a DocuSign permission.
type permissionDefinition struct {
	ID          string
	DisplayName string
	Description string
}

// permissionMapping maps DocuSign user settings fields to permission IDs.
type permissionMapping struct {
	FieldName    string
	PermissionID string
}

// permissionResourceID is the singleton ID for the permissions resource.
const (
	permissionResourceID = "docusign-permissions"
)

// permissionDefinitions contains all possible DocuSign permissions with their metadata.
var permissionDefinitions = []permissionDefinition{
	{"adminOnly", "Admin Only Actions", "Indicates some actions are exclusive for admins"},
	{"canManageAccount", "Manage Account", "Can manage account settings"},
	{"canManageTemplates", "Manage Templates", "Can manage shared templates"},
	{"canEditSharedAddressbook", "Edit Shared Addressbook", "Can edit shared address book"},
	{"canManageOrganization", "Manage Organization", "Can manage organization settings"},
	{"canManageDistributor", "Manage Distributor", "Can manage distributor settings"},
	{"canSendEnvelope", "Send Envelope", "Can send envelopes"},
	{"canSignEnvelope", "Sign Envelope", "Can sign envelopes"},
	{"allowSendOnBehalfOf", "Send On Behalf Of", "Can send envelopes on behalf of others"},
	{"bulkSend", "Bulk Send", "Can send envelopes in bulk"},
	{"canSendAPIRequests", "Send API Requests", "Can make API requests"},
	{"enableSequentialSigningUI", "Sequential Signing UI", "Can use sequential signing UI"},
	{"enableDSPro", "DS Pro Features", "Access to DocuSign Pro features"},
	{"canUseScratchpad", "Use Scratchpad", "Can use scratchpad feature"},
	{"canCreateWorkspaces", "Create Workspaces", "Can create collaborative workspaces"},
	{"enableTransactionPoint", "Transaction Point", "Can use transaction point feature"},
	{"powerFormMode", "PowerForm Admin", "Administrative control over PowerForms"},
	{"apiCanExportAC", "Export Audit Certificates", "Can export audit certificates via API"},
	{"enableVaulting", "Vaulting Access", "Can use long-term storage (Vaulting)"},
	{"canUseSmartContracts", "Smart Contracts", "Can use smart contracts"},
}

// fieldToPermissionMappings maps user setting fields to permission IDs.
var fieldToPermissionMappings = []permissionMapping{
	{"isAdmin", "adminOnly"},
	{"adminOnly", "adminOnly"},
	{"canManageAccount", "canManageAccount"},
	{"canManageTemplates", "canManageTemplates"},
	{"canEditSharedAddressbook", "canEditSharedAddressbook"},
	{"canManageOrganization", "canManageOrganization"},
	{"canManageDistributor", "canManageDistributor"},
	{"canSendEnvelope", "canSendEnvelope"},
	{"canSignEnvelope", "canSignEnvelope"},
	{"allowSendOnBehalfOf", "allowSendOnBehalfOf"},
	{"bulkSend", "bulkSend"},
	{"canSendAPIRequests", "canSendAPIRequests"},
	{"enableSequentialSigningUI", "enableSequentialSigningUI"},
	{"enableDSPro", "enableDSPro"},
	{"canUseScratchpad", "canUseScratchpad"},
	{"canCreateWorkspaces", "canCreateWorkspaces"},
	{"enableTransactionPoint", "enableTransactionPoint"},
	{"powerFormMode", "powerFormMode"},
	{"apiCanExportAC", "apiCanExportAC"},
	{"enableVaulting", "enableVaulting"},
	{"canUseSmartContracts", "canUseSmartContracts"},
}

// validPermissionValues defines acceptable values for permission settings.
var validPermissionValues = map[string]bool{
	"true":  true,
	"admin": true,
	"share": true,
}
