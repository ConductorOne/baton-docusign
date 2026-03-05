package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	IsDemoField = field.BoolField(
		"demo",
		field.WithDisplayName("Demo Environment"),
		field.WithDescription("Set to true for demo environment, false for production"),
		field.WithDefaultValue(false),
	)

	ClientIdField = field.StringField(
		"docusign-client-id",
		field.WithDisplayName("Client ID"),
		field.WithDescription("OAuth 2.0 Client ID from DocuSign"),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	ClientSecretField = field.StringField(
		"docusign-client-secret",
		field.WithDisplayName("Client Secret"),
		field.WithDescription("OAuth 2.0 Client Secret from DocuSign"),
		field.WithIsSecret(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
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
		field.WithDisplayName("Account ID"),
		field.WithDescription("The DocuSign account ID to sync. If not specified, the default account (or the first account) associated with the authenticated user will be used."),
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
		// Note: ClientIdField, ClientSecretField, and RedirectURIField are required together,
		// but RefreshTokenField is conditionally required (not needed during --configure flow).
		// Programmatic validation is handled in connector.go.
		field.FieldsRequiredTogether(ClientIdField, ClientSecretField, RedirectURIField)}
)

//go:generate go run ./gen
var ConfigurationSchema = field.NewConfiguration(
	ConfigurationFields,
	field.WithConstraints(FieldRelationships...),
	field.WithConnectorDisplayName("DocuSign"),
	field.WithHelpUrl("/docs/baton/docusign"),
	field.WithIconUrl("/static/app-icons/docusign.svg"),
)
