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

// permissionBuilder handles the construction of permission-related resources and grants
type permissionBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

// permissionDefinition defines the structure of a DocuSign permission
type permissionDefinition struct {
	ID          string
	DisplayName string
	Description string
	Purpose     v2.Entitlement_PurposeValue
}

// permissionMapping maps DocuSign user settings fields to permission IDs
type permissionMapping struct {
	FieldName    string
	PermissionID string
}

const (
	permissionResourceID = "docusign-permissions"
)

var (
	// permissionDefinitions contains all possible DocuSign permissions with their metadata
	permissionDefinitions = []permissionDefinition{
		{"adminOnly", "Admin Only Actions", "Indicates some actions are exclusive for admins", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageAccount", "Manage Account", "Can manage account settings", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageTemplates", "Manage Templates", "Can manage shared templates", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canEditSharedAddressbook", "Edit Shared Addressbook", "Can edit shared address book", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageOrganization", "Manage Organization", "Can manage organization settings", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageDistributor", "Manage Distributor", "Can manage distributor settings", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canSendEnvelope", "Send Envelope", "Can send envelopes", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canSignEnvelope", "Sign Envelope", "Can sign envelopes", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"allowSendOnBehalfOf", "Send On Behalf Of", "Can send envelopes on behalf of others", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"bulkSend", "Bulk Send", "Can send envelopes in bulk", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canSendAPIRequests", "Send API Requests", "Can make API requests", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"enableSequentialSigningUI", "Sequential Signing UI", "Can use sequential signing UI", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"enableDSPro", "DS Pro Features", "Access to DocuSign Pro features", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canUseScratchpad", "Use Scratchpad", "Can use scratchpad feature", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canCreateWorkspaces", "Create Workspaces", "Can create collaborative workspaces", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"enableTransactionPoint", "Transaction Point", "Can use transaction point feature", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"powerFormMode", "PowerForm Admin", "Administrative control over PowerForms", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"apiCanExportAC", "Export Audit Certificates", "Can export audit certificates via API", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"enableVaulting", "Vaulting Access", "Can use long-term storage (Vaulting)", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canUseSmartContracts", "Smart Contracts", "Can use smart contracts", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
	}

	// fieldToPermissionMappings maps user setting fields to permission IDs
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

	// validPermissionValues defines acceptable values for permission settings
	validPermissionValues = map[string]bool{
		"true":  true,
		"admin": true,
		"share": true,
	}
)

// ResourceType returns the resource type this builder manages (docusign-permissions)
func (p *permissionBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return permissionResourceType
}

// List returns the singleton permission resource that represents all DocuSign permissions
func (p *permissionBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	permissionResource, err := resource.NewRoleResource(
		permissionResourceID,
		permissionResourceType,
		permissionResourceID,
		nil,
	)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to create permission resource: %w", err)
	}

	return []*v2.Resource{permissionResource}, "", nil, nil
}

// Entitlements generates all possible permission entitlements for the permission resource
func (p *permissionBuilder) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	entitlements := make([]*v2.Entitlement, 0, len(permissionDefinitions))

	for _, perm := range permissionDefinitions {
		entitlements = append(entitlements, entitlement.NewPermissionEntitlement(
			resource,
			perm.ID,
			entitlement.WithDisplayName(perm.DisplayName),
			entitlement.WithDescription(perm.Description),
			entitlement.WithGrantableTo(userResourceType),
		))
	}

	return entitlements, "", nil, nil
}

// Grants fetches all users and generates grants for their actual permissions
func (p *permissionBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, annos, err := p.client.GetAllUsersWithDetails(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get users with details: %w", err)
	}

	permissionResource, _, _, err := p.List(ctx, nil, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get permission resource: %w", err)
	}

	grants := make([]*v2.Grant, 0)
	for _, user := range users {
		userGrants, err := p.createUserGrants(permissionResource[0], user)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create grants for user %s: %w", user.UserID, err)
		}
		grants = append(grants, userGrants...)
	}

	return grants, "", annos, nil
}

// createUserGrants generates grants for a single user based on their settings
func (p *permissionBuilder) createUserGrants(permissionResource *v2.Resource, user *client.UserDetail) ([]*v2.Grant, error) {
	settingsMap, err := p.parseUserSettings(user.UserSettings)
	if err != nil {
		return nil, err
	}

	userResource, err := p.createUserResource(user)
	if err != nil {
		return nil, err
	}

	var grants []*v2.Grant
	for _, mapping := range fieldToPermissionMappings {
		if value, exists := settingsMap[mapping.FieldName]; exists {
			if hasPermission, accessLevel := p.checkPermissionValue(value); hasPermission {
				grants = append(grants, grant.NewGrant(
					permissionResource,
					mapping.PermissionID,
					userResource.Id,
					grant.WithGrantMetadata(p.createGrantMetadata(user, accessLevel)),
				),
				)
			}
		}
	}

	return grants, nil
}

// parseUserSettings converts user settings interface into a map for easier processing
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

// createUserResource creates a user resource from DocuSign user details
func (p *permissionBuilder) createUserResource(user *client.UserDetail) (*v2.Resource, error) {
	return resource.NewUserResource(
		user.UserName,
		userResourceType,
		user.UserID,
		[]resource.UserTraitOption{
			resource.WithUserProfile(map[string]interface{}{
				"email":      user.Email,
				"isAdmin":    user.IsAdmin,
				"permission": user.PermissionProfileName,
			}),
			resource.WithStatus(p.getUserStatus(user.UserStatus)),
		},
	)
}

// createGrantMetadata creates metadata for permission grants
func (p *permissionBuilder) createGrantMetadata(user *client.UserDetail, accessLevel string) map[string]interface{} {
	return map[string]interface{}{
		"source":       "DocuSign",
		"profile":      user.PermissionProfileName,
		"user_id":      user.UserID,
		"username":     user.UserName,
		"access_level": accessLevel,
	}
}

// checkPermissionValue validates and normalizes a permission value
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

// getUserStatus converts DocuSign user status to Baton status enum
func (p *permissionBuilder) getUserStatus(status string) v2.UserTrait_Status_Status {
	switch strings.ToLower(status) {
	case "active":
		return v2.UserTrait_Status_STATUS_ENABLED
	case "inactive", "deactivated":
		return v2.UserTrait_Status_STATUS_DISABLED
	default:
		return v2.UserTrait_Status_STATUS_UNSPECIFIED
	}
}

// newPermissionBuilder creates a new permissionBuilder instance
func newPermissionBuilder(client *client.Client) *permissionBuilder {
	return &permissionBuilder{
		resourceType: permissionResourceType,
		client:       client,
	}
}
