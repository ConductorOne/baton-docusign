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
		field.WithIsRequired(true),
	)

	ClientSecretField = field.StringField(
		"docusign-client-secret",
		field.WithDisplayName("Client Secret"),
		field.WithDescription("OAuth 2.0 Client Secret from your DocuSign developer app"),
		field.WithIsSecret(true),
		field.WithIsRequired(true),
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
			Fields:      []field.SchemaField{Oauth2TokenField, AccountIdField, IncludeSigningGroupsField},
		},
		{
			Name:        "demo",
			DisplayName: "Custom App (Demo Environment)",
			HelpText: "Authenticate using your own DocuSign developer app against DocuSign's demo environment. " +
				"Provide your app's Client ID and Client Secret, then click the OAuth button to authorize.",
			Fields: []field.SchemaField{ClientIdField, ClientSecretField, Oauth2TokenField, AccountIdField, IncludeSigningGroupsField},
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
