package connector

import (
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
)

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
		Id:          "permission_profile",
		DisplayName: "Permission Profile",
	}

	signingGroupResourceType = &v2.ResourceType{
		Id:          "signing_group",
		DisplayName: "Signing Group",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_GROUP},
	}

	// CLM (Contract Lifecycle Management) resource types. CLM is a separate DocuSign
	// product/API surface from eSignature above. Synced only when the include-clm
	// opt-in flag is set.

	// clmMemberResourceType is CLM's own principal object. Deliberately NOT reusing
	// userResourceType's id ("user") — the CLM Members API is a distinct upstream
	// object, and 1:1 identity with the eSignature user could not be confirmed.
	clmMemberResourceType = &v2.ResourceType{
		Id:          "clm_member",
		DisplayName: "CLM Member",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_USER},
	}

	// clmRoleResourceType represents the 5 fixed CLM account-level Member.Role values.
	// Not backed by its own API call (see client.ClmRoles) — needed so folder-security
	// entries granted "by role" have a real synced resource to grant against.
	clmRoleResourceType = &v2.ResourceType{
		Id:          "clm_role",
		DisplayName: "CLM Role",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_ROLE},
	}

	// clmGroupResourceType is a distinct upstream object from groupResourceType
	// ("group", eSignature) — deliberately not reusing that id.
	clmGroupResourceType = &v2.ResourceType{
		Id:          "clm_group",
		DisplayName: "CLM Group",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_GROUP},
	}

	// clmPermissionSetResourceType is read-only for GRANTS — no assignment endpoint
	// exists anywhere in the CLM API. Entitlements still sync normally (mirrors
	// permissionProfilesResourceType's precedent, which also carries no skip
	// annotation) — only Grants() returns nil.
	clmPermissionSetResourceType = &v2.ResourceType{
		Id:          "clm_permission_set",
		DisplayName: "CLM Permission Set",
		Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_ROLE},
	}

	// clmFolderResourceType represents CLM folders and their security assignments
	// (folder security is modeled as this resource type's Entitlements/Grants).
	clmFolderResourceType = &v2.ResourceType{
		Id:          "clm_folder",
		DisplayName: "CLM Folder",
		Annotations: annotations.New(&v2.SkipEntitlements{}),
	}
)
