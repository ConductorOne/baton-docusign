package connector

import (
	"strings"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
)

// parsePageToken deserializes the Baton token and returns the Bag and page number for upstream.
func parsePageToken(i string, resourceID *v2.ResourceId) (*pagination.Bag, string, error) {
	b := &pagination.Bag{}
	if err := b.Unmarshal(i); err != nil {
		return nil, "", err
	}

	if b.Current() == nil {
		b.Push(pagination.PageState{
			ResourceTypeID: resourceID.ResourceType,
			ResourceID:     resourceID.Resource,
		})
	}

	return b, b.PageToken(), nil
}

// createUserGrants generates grants for a single user based on their settings.
func createUserGrants(permissionResource *v2.Resource, user *client.UserDetail) ([]*v2.Grant, error) {
	settingsMap, err := parseUserSettings(user.UserSettings)
	if err != nil {
		return nil, err
	}

	var grants []*v2.Grant
	for _, mapping := range fieldToPermissionMappings {
		if value, exists := settingsMap[mapping.FieldName]; exists {
			if hasPermission, accessLevel := checkPermissionValue(value); hasPermission {
				subject := makeUserSubjectID(user.UserID)
				grants = append(grants, grant.NewGrant(
					permissionResource,
					mapping.PermissionID,
					subject,
					grant.WithGrantMetadata(createGrantMetadata(user, accessLevel)),
				))
			}
		}
	}

	return grants, nil
}

// createGrantMetadata creates metadata for permission grants.
func createGrantMetadata(user *client.UserDetail, accessLevel string) map[string]interface{} {
	return map[string]interface{}{
		"source":       "DocuSign",
		"profile":      user.PermissionProfileName,
		"user_id":      user.UserID,
		"username":     user.UserName,
		"access_level": accessLevel,
	}
}

// checkPermissionValue validates and normalizes a permission value.
func checkPermissionValue(value interface{}) (bool, string) {
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
