package main

import (
	"context"
	"fmt"
	"os"

	cfg "github.com/conductorone/baton-docusign/pkg/config"
	"github.com/conductorone/baton-docusign/pkg/connector"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

var version = "dev"

func main() {
	ctx := context.Background()

	_, cmd, err := config.DefineConfiguration(
		ctx,
		"baton-docusign",
		getConnector,
		cfg.ConfigurationSchema,
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

func getConnector(ctx context.Context, docusignCfg *cfg.Docusign) (types.ConnectorServer, error) {
	l := ctxzap.Extract(ctx)

	// Validate the configuration
	if err := field.Validate(cfg.ConfigurationSchema, docusignCfg); err != nil {
		return nil, err
	}

	// In production, `docusignCfg.Configure` is always false.
	if docusignCfg.Configure {
		if err := connector.Configure(ctx, docusignCfg); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}

	if docusignCfg.RefreshToken == "" {
		return nil, fmt.Errorf("refresh token is required, get it by running the connector with the --configure flag")
	}

	cb, err := connector.New(
		ctx,
		docusignCfg.Demo,
		docusignCfg.ClientId,
		docusignCfg.ClientSecret,
		docusignCfg.RedirectUri,
		docusignCfg.RefreshToken,
		docusignCfg.IncludeSigningGroups,
	)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	connectorObj, err := connectorbuilder.NewConnector(ctx, cb)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	return connectorObj, nil
}
