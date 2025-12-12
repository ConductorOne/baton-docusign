package main

import (
	cfg "github.com/conductorone/baton-docusign/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/config"
)

func main() {
	config.Generate("docusign", cfg.ConfigurationSchema)
}
