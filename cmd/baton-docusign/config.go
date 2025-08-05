package main

import (
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/spf13/viper"
)

var (
	isDemoField = field.BoolField(
		"demo",
		field.WithDescription("Set to true for demo environment, false for production"),
		field.WithDefaultValue(true),
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
		field.WithDescription("Refresh token."),
		field.WithRequired(true),
	)

	ConfigurationFields = []field.SchemaField{
		isDemoField,
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
