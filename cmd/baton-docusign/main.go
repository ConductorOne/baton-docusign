package main

import (
	"context"
	"fmt"
	"os"

	connectorSchema "github.com/conductorone/baton-docusign/pkg/connector"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var version = "dev"

func main() {
	ctx := context.Background()

	_, cmd, err := config.DefineConfiguration(
		ctx,
		"baton-docusign",
		getConnector,
		field.Configuration{
			Fields: ConfigurationFields,
		},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	cmd.Version = version

	err = cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func getConnector(ctx context.Context, v *viper.Viper) (types.ConnectorServer, error) {
	l := ctxzap.Extract(ctx)
	if err := ValidateConfig(v); err != nil {
		return nil, err
	}

	docusignApi := v.GetString(apiUrlField.FieldName)
	docusignAccount := v.GetString(accountField.FieldName)
	docusignClientId := v.GetString(clientIdField.FieldName)
	docusignClientSecret := v.GetString(clientSecretField.FieldName)
	docusignRedirectURI := v.GetString(redirectURIField.FieldName)
	docusignRefreshToken := v.GetString(refreshTokenField.FieldName)

	cb, err := connectorSchema.New(ctx, docusignApi, docusignAccount, docusignClientId, docusignClientSecret, docusignRedirectURI, docusignRefreshToken)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}
	connector, err := connectorbuilder.NewConnector(ctx, cb)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}
	return connector, nil
}
