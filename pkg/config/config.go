package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	IsDemoField = field.BoolField(
		"demo",
		field.WithDisplayName("Demo Environment"),
		field.WithDescription("Set to true for demo environment, false for production"),
		field.WithDefaultValue(true),
	)

	ClientIdField = field.StringField(
		"clientId",
		field.WithDisplayName("Client ID"),
		field.WithDescription("OAuth 2.0 Client ID from DocuSign"),
		field.WithRequired(true),
	)

	ClientSecretField = field.StringField(
		"clientSecret",
		field.WithDisplayName("Client Secret"),
		field.WithDescription("OAuth 2.0 Client Secret from DocuSign"),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)

	RedirectURIField = field.StringField(
		"redirect-uri",
		field.WithDisplayName("Redirect URI"),
		field.WithDescription("Redirect URI registered in your DocuSign integration"),
		field.WithRequired(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	RefreshTokenField = field.StringField(
		"refresh-token",
		field.WithDisplayName("Refresh Token"),
		field.WithDescription("OAuth 2.0 Refresh Token for DocuSign"),
		field.WithRequired(false),
		field.WithIsSecret(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	ConfigureField = field.BoolField(
		"configure",
		field.WithDisplayName("Configure"),
		field.WithDescription("Get the refresh token the first time you run the connector."),
		field.WithRequired(false),
		field.WithExportTarget(field.ExportTargetCLIOnly),
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
		IncludeSigningGroupsField,
		Oauth2TokenField,
	}
)

//go:generate go run ./gen
var ConfigurationSchema = field.NewConfiguration(
	ConfigurationFields,
	field.WithConnectorDisplayName("DocuSign"),
	field.WithHelpUrl("/docs/baton/docusign"),
	field.WithIconUrl("/static/app-icons/docusign.svg"),
)
