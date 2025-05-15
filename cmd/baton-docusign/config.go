package main

import (
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/spf13/viper"
)

var (
	apiUrlField = field.StringField(
		"api-url",
		field.WithDescription("The base URL of the DocuSign API"),
		field.WithDefaultValue("https://demo.docusign.net"),
	)

	accountField = field.StringField(
		"account-id",
		field.WithDescription("Your DocuSign account ID"),
		field.WithRequired(true),
	)

	clientIdField = field.StringField(
		"clientId",
		field.WithDescription("OAuth 2.0 Client ID from DocuSign"),
		field.WithRequired(true),
	)

	clientSecretField = field.StringField(
		"clientSecret",
		field.WithDescription("OAuth 2.0 Client Secret from DocuSign"),
		field.WithRequired(true),
	)

	redirectURIField = field.StringField(
		"redirect-uri",
		field.WithDescription("Redirect URI registered in your DocuSign integration"),
		field.WithRequired(true),
	)
	refreshTokenField = field.StringField(
		"refresh-token",
		field.WithDescription("Optional. Refresh token."),
	)

	ConfigurationFields = []field.SchemaField{
		apiUrlField,
		accountField,
		clientIdField,
		clientSecretField,
		redirectURIField,
		refreshTokenField,
	}

	FieldRelationships = []field.SchemaFieldRelationship{}
)

func ValidateConfig(v *viper.Viper) error {
	return nil
}
