package main

import (
	"context"

	cfg "github.com/conductorone/baton-docusign/pkg/config"
	"github.com/conductorone/baton-docusign/pkg/connector"
	"github.com/conductorone/baton-sdk/pkg/config"
)

var version = "dev"

func main() {
	ctx := context.Background()

	config.RunConnector(
		ctx,
		"baton-docusign",
		version,
		cfg.ConfigurationSchema,
		connector.New,
	)
}
