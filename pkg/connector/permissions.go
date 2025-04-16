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

type permissionBuilder struct {
	resourceType *v2.ResourceType
	client       *client.Client
}

type permissionDefinition struct {
	ID          string
	DisplayName string
	Description string
	Purpose     v2.Entitlement_PurposeValue
}

func getPermissionDefinitions() []permissionDefinition {
	return []permissionDefinition{
		{"adminOnly", "Admin Only Actions", "Indicates some actions are exclusive for admins", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageAccount", "Manage Account", "Can manage account settings", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageTemplates", "Manage Templates", "Can manage shared templates", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canEditSharedAddressbook", "Edit Shared Addressbook", "Can edit shared address book", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageOrganization", "Manage Organization", "Can manage organization settings", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canManageDistributor", "Manage Distributor", "Can manage distributor settings", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
		{"canSendEnvelope", "Send Envelope", "Can send envelopes (documents for signing)", v2.Entitlement_PURPOSE_VALUE_PERMISSION},
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
}

func (p *permissionBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return permissionResourceType
}

func createPermissionResource() (*v2.Resource, error) {
	return resource.NewRoleResource(
		"docusign-permissions",
		permissionResourceType,
		"docusign-permissions",
		nil,
	)
}

func createUserResource(user *client.UserDetail) (*v2.Resource, error) {
	userTraits := []resource.UserTraitOption{
		resource.WithUserProfile(map[string]interface{}{
			"email":      user.Email,
			"isAdmin":    user.IsAdmin,
			"permission": user.PermissionProfileName,
		}),
		resource.WithStatus(getUserStatus(user.UserStatus)),
	}

	return resource.NewUserResource(
		user.UserName,
		userResourceType,
		user.UserID,
		userTraits,
	)
}

func (p *permissionBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	permissionResource, err := createPermissionResource()
	if err != nil {
		return nil, "", nil, err
	}

	return []*v2.Resource{permissionResource}, "", nil, nil
}

func (p *permissionBuilder) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var ents []*v2.Entitlement

	for _, perm := range getPermissionDefinitions() {
		e := entitlement.NewPermissionEntitlement(
			resource,
			perm.ID,
			entitlement.WithDisplayName(perm.DisplayName),
			entitlement.WithDescription(perm.Description),
			entitlement.WithGrantableTo(userResourceType),
		)
		ents = append(ents, e)
	}

	return ents, "", nil, nil
}

func (p *permissionBuilder) Grants(ctx context.Context, parentResource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var grants []*v2.Grant
	annos := annotations.Annotations{}

	users, newAnnos, err := p.client.GetAllUsersWithDetails(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("error getting users with details: %w", err)
	}
	for _, a := range newAnnos {
		annos.Append(a)
	}

	permissionResource, err := createPermissionResource()
	if err != nil {
		return nil, "", nil, err
	}

	for _, user := range users {
		userResource, err := createUserResource(user)
		if err != nil {
			return nil, "", nil, err
		}

		userGrants, err := p.processUserPermissions(permissionResource, userResource, user)
		if err != nil {
			return nil, "", nil, err
		}

		grants = append(grants, userGrants...)
	}

	return grants, "", annos, nil
}

func (p *permissionBuilder) processUserPermissions(permissionResource, userResource *v2.Resource, user *client.UserDetail) ([]*v2.Grant, error) {
	var grants []*v2.Grant

	settingsJSON, err := json.Marshal(user.UserSettings)
	if err != nil {
		return nil, fmt.Errorf("error marshaling user settings: %w", err)
	}

	settingsMap := make(map[string]interface{})
	if err := json.Unmarshal(settingsJSON, &settingsMap); err != nil {
		return nil, fmt.Errorf("error unmarshaling user settings: %w", err)
	}

	fieldToPermissionID := map[string]string{
		"isAdmin":                   "adminOnly",
		"adminOnly":                 "adminOnly",
		"canManageAccount":          "canManageAccount",
		"canManageTemplates":        "canManageTemplates",
		"canEditSharedAddressbook":  "canEditSharedAddressbook",
		"canManageOrganization":     "canManageOrganization",
		"canManageDistributor":      "canManageDistributor",
		"canSendEnvelope":           "canSendEnvelope",
		"canSignEnvelope":           "canSignEnvelope",
		"allowSendOnBehalfOf":       "allowSendOnBehalfOf",
		"bulkSend":                  "bulkSend",
		"canSendAPIRequests":        "canSendAPIRequests",
		"enableSequentialSigningUI": "enableSequentialSigningUI",
		"enableDSPro":               "enableDSPro",
		"canUseScratchpad":          "canUseScratchpad",
		"canCreateWorkspaces":       "canCreateWorkspaces",
		"enableTransactionPoint":    "enableTransactionPoint",
		"powerFormMode":             "powerFormMode",
		"apiCanExportAC":            "apiCanExportAC",
		"enableVaulting":            "enableVaulting",
		"canUseSmartContracts":      "canUseSmartContracts",
	}

	for _, perm := range getPermissionDefinitions() {
		for field, permID := range fieldToPermissionID {
			if permID == perm.ID {
				if value, ok := settingsMap[field]; ok {
					if hasPermission, accessLevel := p.checkPermissionValue(value); hasPermission {
						g := grant.NewGrant(
							permissionResource,
							perm.ID,
							userResource.Id,
							grant.WithGrantMetadata(map[string]interface{}{
								"source":       "DocuSign",
								"profile":      user.PermissionProfileName,
								"user_id":      user.UserID,
								"username":     user.UserName,
								"access_level": accessLevel,
							}),
						)
						grants = append(grants, g)
						break
					}
				}
			}
		}
	}

	return grants, nil
}

func (p *permissionBuilder) checkPermissionValue(value interface{}) (bool, string) {
	validValues := map[string]bool{
		"true":  true,
		"admin": true,
		"share": true,
	}

	switch v := value.(type) {
	case string:
		lowerVal := strings.ToLower(v)
		return validValues[lowerVal], lowerVal
	case bool:
		if v {
			return true, "true"
		}
		return false, "false"
	default:
		return false, ""
	}
}

func getUserStatus(status string) v2.UserTrait_Status_Status {
	switch strings.ToLower(status) {
	case "active":
		return v2.UserTrait_Status_STATUS_ENABLED
	case "inactive", "deactivated":
		return v2.UserTrait_Status_STATUS_DISABLED
	default:
		return v2.UserTrait_Status_STATUS_UNSPECIFIED
	}
}

func newPermissionBuilder(client *client.Client) *permissionBuilder {
	return &permissionBuilder{
		resourceType: permissionResourceType,
		client:       client,
	}
}
