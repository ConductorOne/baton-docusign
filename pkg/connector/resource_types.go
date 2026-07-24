package connector

import (
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
)

// PermissionProfileResourceTypeID is the resource type ID for permission profiles.
// Exported so other packages (e.g. connector.go) can check whether it is
// selected for sync via cli.ConnectorOpts.WillSyncResourceType without
// depending on an unexported literal drifting out of sync.
const PermissionProfileResourceTypeID = "permission_profile"

var (
	userResourceType = &v2.ResourceType{
		Id:          "user",
		DisplayName: "User",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_USER},
	}

	groupResourceType = &v2.ResourceType{
		Id:          "group",
		DisplayName: "Group",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_GROUP},
	}

	permissionProfilesResourceType = &v2.ResourceType{
		Id:          PermissionProfileResourceTypeID,
		DisplayName: "Permission Profile",
	}

	signingGroupResourceType = &v2.ResourceType{
		Id:          "signing_group",
		DisplayName: "Signing Group",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_GROUP},
	}
)
