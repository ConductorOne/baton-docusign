package main

import (
	"context"
	"os"

	cfg "github.com/conductorone/baton-docusign/pkg/config"
	"github.com/conductorone/baton-docusign/pkg/connector"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
)

var version = "dev"

func main() {
	ctx := context.Background()

	// Wrap the connector.New to handle configure completion
	connectorFn := func(ctx context.Context, docusignCfg *cfg.Docusign, opts *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
		cb, cOpts, err := connector.New(ctx, docusignCfg, opts)
		if err != nil && err.Error() == "configuration complete" {
			// Configure flow completed successfully, exit with success
			os.Exit(0)
		}
		return cb, cOpts, err
	}

	config.RunConnector(
		ctx,
		"baton-docusign",
		version,
		cfg.ConfigurationSchema,
		connectorFn,
	)
}
