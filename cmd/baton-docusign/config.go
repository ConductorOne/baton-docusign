package main

import (
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/spf13/viper"
)

var (
	apiUrlField = field.StringField(
		"api-url",
		field.WithDescription("The URL of the DocuSign API."),
		field.WithDefaultValue("https://demo.docusign.net"),
	)
	tokenField = field.StringField(
		"token",
		field.WithDescription("The authorization token required for authentication."),
		field.WithRequired(true),
	)
	accountField = field.StringField(
		"account-id",
		field.WithDescription("The DocuSign account ID"),
		field.WithRequired(true),
	)
	clientIdField = field.StringField(
		"clientId",
		field.WithDescription("The authorization client id required for authentication."),
		field.WithRequired(true),
	)
	clientSecretField = field.StringField(
		"clientSecret",
		field.WithDescription("The authorization client secret required for authentication."),
		field.WithRequired(true),
	)
	refreshTokenField = field.StringField(
		"refresh-token",
		field.WithDescription("The authorization refresh token required for authentication."),
		field.WithRequired(true),
	)

	ConfigurationFields = []field.SchemaField{apiUrlField, tokenField, accountField, clientIdField, clientSecretField, refreshTokenField}

	FieldRelationships = []field.SchemaFieldRelationship{}
)

// ValidateConfig is run after the configuration is loaded, and should return an
// error if it isn't valid. Implementing this function is optional, it only
// needs to perform extra validations that cannot be encoded with configuration
// parameters.
func ValidateConfig(v *viper.Viper) error {
	return nil
}
