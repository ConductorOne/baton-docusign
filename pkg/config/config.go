package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	// IsDemoField is CLI-only: demo mode is implied in the GUI by selecting the "demo" field group.
	IsDemoField = field.BoolField(
		"demo",
		field.WithDisplayName("Demo Environment"),
		field.WithDescription("Set to true for demo environment, false for production"),
		field.WithDefaultValue(false),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	ClientIdField = field.StringField(
		"docusign-client-id",
		field.WithDisplayName("Client ID"),
		field.WithDescription("OAuth 2.0 Client ID from your DocuSign developer app"),
		field.WithRequired(true),
	)

	ClientSecretField = field.StringField(
		"docusign-client-secret",
		field.WithDisplayName("Client Secret"),
		field.WithDescription("OAuth 2.0 Client Secret from your DocuSign developer app"),
		field.WithIsSecret(true),
		field.WithRequired(true),
	)

	RedirectURIField = field.StringField(
		"redirect-uri",
		field.WithDisplayName("Redirect URI"),
		field.WithDescription("Redirect URI registered in your DocuSign integration"),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	RefreshTokenField = field.StringField(
		"refresh-token",
		field.WithDisplayName("Refresh Token"),
		field.WithDescription("OAuth 2.0 Refresh Token for DocuSign"),
		field.WithIsSecret(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	ConfigureField = field.BoolField(
		"configure",
		field.WithDisplayName("Configure"),
		field.WithDescription("Get the refresh token the first time you run the connector."),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	AccountIdField = field.StringField(
		"account-id",
		field.WithDisplayName("DocuSign Account ID"),
		field.WithDescription("API account ID (UUID format) of the DocuSign account to be used for synchronization. "+
			"Leave blank to use your default account. "+
			"Warning: changing this ID between different synchronizations may result in data loss. "+
			"If you want to synchronize different accounts, create different connectors."),
	)

	IncludeSigningGroupsField = field.BoolField(
		"include-signing-groups",
		field.WithDisplayName("Include Signing Groups"),
		field.WithDescription("Set to true to include syncing signing groups (for customers with signing groups feature enabled)"),
		field.WithDefaultValue(false),
	)

	IncludeClmField = field.BoolField(
		"include-clm",
		field.WithDisplayName("Include CLM"),
		field.WithDescription("Set to true to include syncing DocuSign CLM folders, folder security, groups, and permission sets. Requires a DocuSign CLM production subscription. "+
			"When using the default OAuth Authentication method, this also requires ConductorOne's managed OAuth app to be granted the CLM API scope — "+
			"contact ConductorOne if enabling this has no effect."),
		field.WithDefaultValue(false),
	)

	// ClmBaseURLField is ops-only and for testing: it lets ConductorOne's own
	// deployment/support tooling point the connector at a local CLM mock (see
	// cmd/test-server) when the authenticated account has no CLM subscription of its
	// own, without changing how eSignature calls are resolved. Hidden from GUI config
	// and CLI --help — a real tenant admin has no legitimate reason to set this.
	ClmBaseURLField = field.StringField(
		"clm-base-url",
		field.WithDisplayName("CLM Base URL Override"),
		field.WithDescription("Testing only: overrides the DocuSign CLM API base URL instead of resolving it from the OAuth token's api_base_url. "+
			"Use to point CLM API calls at a local mock server (see cmd/test-server) when the connected account has no CLM subscription."),
		field.WithExportTarget(field.ExportTargetOps),
		field.WithHidden(true),
	)

	// BaseURLField is ops-only and for testing: it lets ConductorOne's own
	// deployment/support tooling run the connector entirely against a local mock (see
	// cmd/test-server), with no contact with real DocuSign at all. Setting this also
	// skips the real OAuth token refresh — refresh-token is used verbatim as a static
	// bearer token, since there's no real account to refresh against once eSignature
	// calls are redirected to a mock. Combine with clm-base-url (and
	// --sync-resource-types to skip resource types the mock doesn't serve) to fully
	// isolate a test run from DocuSign. Hidden from GUI config and CLI --help.
	BaseURLField = field.StringField(
		"base-url",
		field.WithDisplayName("Base URL Override"),
		field.WithDescription("Testing only: overrides the DocuSign eSignature API base URL and skips the real OAuth token refresh "+
			"(refresh-token is used verbatim as a static bearer token instead). Use with clm-base-url to point the whole connector at a local mock server (see cmd/test-server)."),
		field.WithExportTarget(field.ExportTargetOps),
		field.WithHidden(true),
	)

	Oauth2TokenField = field.Oauth2Field(
		"oauth2-token",
		field.WithDisplayName("OAuth Authentication"),
		field.WithDescription("OAuth 2.0 Authentication for DocuSign"),
	)

	// ConfigurationFields defines the external configuration required for the
	// connector to run. Note: these fields can be marked as optional or
	// required.
	ConfigurationFields = []field.SchemaField{
		IsDemoField,
		ClientIdField,
		ClientSecretField,
		RedirectURIField,
		RefreshTokenField,
		ConfigureField,
		AccountIdField,
		IncludeSigningGroupsField,
		IncludeClmField,
		ClmBaseURLField,
		BaseURLField,
		Oauth2TokenField,
	}

	// FieldRelationships defines relationships between the ConfigurationFields that can be automatically validated.
	// For example, a username and password can be required together, or an access token can be
	// marked as mutually exclusive from the username password pair.
	FieldRelationships = []field.SchemaFieldRelationship{
		field.FieldsMutuallyExclusive(RefreshTokenField, Oauth2TokenField),
	}

	// FieldGroups defines how fields are presented in the C1 UI.
	// The "oauth2" group (default) uses ConductorOne's managed DocuSign OAuth app for production.
	// The "demo" group lets customers supply their own DocuSign developer app credentials
	// against DocuSign's demo environment.
	FieldGroups = []field.SchemaFieldGroup{
		{
			Name:        "oauth2",
			DisplayName: "OAuth Authentication",
			HelpText:    "Authenticate using ConductorOne's managed DocuSign OAuth app (production environment).",
			Default:     true,
			Fields:      []field.SchemaField{Oauth2TokenField, AccountIdField, IncludeSigningGroupsField, IncludeClmField},
		},
		{
			Name:        "demo",
			DisplayName: "Custom App (Demo Environment)",
			HelpText: "Authenticate using your own DocuSign developer app against DocuSign's demo environment. " +
				"Provide your app's Client ID and Client Secret, then click the OAuth button to authorize.",
			Fields: []field.SchemaField{ClientIdField, ClientSecretField, Oauth2TokenField, AccountIdField, IncludeSigningGroupsField, IncludeClmField},
		},
	}
)

//go:generate go run ./gen
var ConfigurationSchema = field.NewConfiguration(
	ConfigurationFields,
	field.WithConstraints(FieldRelationships...),
	field.WithConnectorDisplayName("DocuSign"),
	field.WithHelpUrl("/docs/baton/docusign"),
	field.WithIconUrl("/static/app-icons/docusign.svg"),
	field.WithFieldGroups(FieldGroups),
)
