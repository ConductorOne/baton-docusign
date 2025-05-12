package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
)

// userFetcher defines an interface to fetch all DocuSign users with detailed settings.
type userFetcher interface {
	GetAllUsersWithDetails(ctx context.Context) ([]*client.UserDetail, annotations.Annotations, error)
}

// permissionBuilder handles the construction of permission-related resources and grants.
type permissionBuilder struct {
	resourceType *v2.ResourceType
	client       userFetcher
}

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
var (
	permissionDefinitions = []permissionDefinition{
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
	fieldToPermissionMappings = []permissionMapping{
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
	validPermissionValues = map[string]bool{
		"true":  true,
		"admin": true,
		"share": true,
	}
)

// ResourceType returns the resource type this builder manages (docusign-permissions).
func (p *permissionBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return permissionResourceType
}

// List returns the singleton permission resource that represents all DocuSign permissions.
func (p *permissionBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	annos := annotations.Annotations{}
	permissionResource, err := resource.NewRoleResource(
		permissionResourceID,
		permissionResourceType,
		permissionResourceID,
		nil,
	)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to create permission resource: %w", err)
	}

	return []*v2.Resource{permissionResource}, "", annos, nil
}

// Entitlements generates all possible permission entitlements for the permission resource.
func (p *permissionBuilder) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	entitlements := make([]*v2.Entitlement, 0, len(permissionDefinitions))
	annos := annotations.Annotations{}
	for _, permission := range permissionDefinitions {
		entitlements = append(entitlements, entitlement.NewPermissionEntitlement(
			resource,
			permission.ID,
			entitlement.WithDisplayName(permission.DisplayName),
			entitlement.WithDescription(permission.Description),
			entitlement.WithGrantableTo(userResourceType),
		))
	}

	return entitlements, "", annos, nil
}

// Grants fetches all users and generates grants for their actual permissions.
func (p *permissionBuilder) Grants(ctx context.Context, permissionResource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, annos, err := p.client.GetAllUsersWithDetails(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get users with details: %w", err)
	}

	var grants []*v2.Grant
	for _, user := range users {
		userGrants, err := p.createUserGrants(permissionResource, user)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create grants for user %s: %w", user.UserID, err)
		}
		grants = append(grants, userGrants...)
	}

	return grants, "", annos, nil
}

// createUserGrants generates grants for a single user based on their settings.
func (p *permissionBuilder) createUserGrants(permissionResource *v2.Resource, user *client.UserDetail) ([]*v2.Grant, error) {
	settingsMap, err := p.parseUserSettings(user.UserSettings)
	if err != nil {
		return nil, err
	}

	var grants []*v2.Grant
	for _, mapping := range fieldToPermissionMappings {
		if value, exists := settingsMap[mapping.FieldName]; exists {
			if hasPermission, accessLevel := p.checkPermissionValue(value); hasPermission {
				subject := makeUserSubjectID(user.UserID)
				grants = append(grants, grant.NewGrant(
					permissionResource,
					mapping.PermissionID,
					subject,
					grant.WithGrantMetadata(p.createGrantMetadata(user, accessLevel)),
				))
			}
		}
	}

	return grants, nil
}

// makeUserSubjectID creates a ResourceId for a user based on their user ID.
func makeUserSubjectID(userID string) *v2.ResourceId {
	return &v2.ResourceId{
		ResourceType: userResourceType.Id,
		Resource:     userID,
	}
}

// parseUserSettings converts user settings interface into a map for easier processing.
func (p *permissionBuilder) parseUserSettings(settings interface{}) (map[string]interface{}, error) {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal user settings: %w", err)
	}

	var settingsMap map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settingsMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user settings: %w", err)
	}

	return settingsMap, nil
}

// createGrantMetadata creates metadata for permission grants.
func (p *permissionBuilder) createGrantMetadata(user *client.UserDetail, accessLevel string) map[string]interface{} {
	return map[string]interface{}{
		"source":       "DocuSign",
		"profile":      user.PermissionProfileName,
		"user_id":      user.UserID,
		"username":     user.UserName,
		"access_level": accessLevel,
	}
}

// checkPermissionValue validates and normalizes a permission value.
func (p *permissionBuilder) checkPermissionValue(value interface{}) (bool, string) {
	switch v := value.(type) {
	case string:
		lowerVal := strings.ToLower(v)
		return validPermissionValues[lowerVal], lowerVal
	case bool:
		if v {
			return true, "true"
		}
		return false, "false"
	default:
		return false, ""
	}
}

// newPermissionBuilder creates a new permissionBuilder instance.
func newPermissionBuilder(client *client.Client) *permissionBuilder {
	return &permissionBuilder{
		resourceType: permissionResourceType,
		client:       client,
	}
}
