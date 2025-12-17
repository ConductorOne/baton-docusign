package connector

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/conductorone/baton-docusign/pkg/client"
	cfg "github.com/conductorone/baton-docusign/pkg/config"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

type Connector struct {
	client               *client.Client
	includeSigningGroups bool
}

// Configure handles the OAuth2 authorization flow to obtain a refresh token.
func Configure(ctx context.Context, docusignCfg *cfg.Docusign) error {
	if docusignCfg.DocusignClientId == "" {
		return fmt.Errorf("client-id is required")
	}

	if docusignCfg.DocusignClientSecret == "" {
		return fmt.Errorf("client-secret is required")
	}

	if docusignCfg.RedirectUri == "" {
		return fmt.Errorf("redirect-uri is required")
	}

	// Create OAuth2 helper for authorization flow
	oauth2Helper := client.NewOAuth2Docusign(docusignCfg.Demo, docusignCfg.DocusignClientId, docusignCfg.DocusignClientSecret, docusignCfg.RedirectUri)

	code, err := oauth2Helper.Authorize(ctx)
	if err != nil {
		return err
	}

	// Exchange the authorization code for tokens
	token, err := oauth2Helper.ExchangeCodeForToken(ctx, code)
	if err != nil {
		return err
	}

	// Validate that we received a refresh token
	if token.RefreshToken == "" {
		return fmt.Errorf("received empty refresh token from DocuSign; check OAuth scopes and app configuration")
	}

	// Output the refresh token to stdout (not logs for security)
	fmt.Fprintf(os.Stdout, "\nrefresh token: %s\n", token.RefreshToken)
	return nil
}

func (d *Connector) ResourceSyncers(_ context.Context) []connectorbuilder.ResourceSyncerV2 {
	syncers := []connectorbuilder.ResourceSyncerV2{
		newUserBuilder(d.client),
		newGroupBuilder(d.client),
		newPermissionProfilesBuilder(d.client),
	}

	// Only include signing groups if opted in
	if d.includeSigningGroups {
		syncers = append(syncers, newSigningGroupBuilder(d.client))
	}

	return syncers
}

func (d *Connector) Asset(_ context.Context, _ *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

func (d *Connector) Metadata(_ context.Context) (*v2.ConnectorMetadata, error) {
	description := "Connector syncs data from Users, Permission Profiles, and Groups. It also allows the creation of users in DocuSign"
	if d.includeSigningGroups {
		description = "Connector syncs data from Users, Permission Profiles, Groups, and Signing Groups. It also allows the creation of users in DocuSign"
	}

	return &v2.ConnectorMetadata{
		DisplayName: "DocuSign",
		Description: description,
		AccountCreationSchema: &v2.ConnectorAccountCreationSchema{
			FieldMap: map[string]*v2.ConnectorAccountCreationSchema_Field{
				"email": {
					DisplayName: "Email",
					Required:    true,
					Description: "This email will be used as the login for the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Email",
					Order:       1,
				},
				"username": {
					DisplayName: "Username",
					Required:    true,
					Description: "This username will be used for the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Username",
					Order:       2,
				},
			},
		},
	}, nil
}

func (d *Connector) Validate(_ context.Context) (annotations.Annotations, error) {
	return nil, nil
}

func NewWithRefreshToken(ctx context.Context, isDemo bool, clientId, clientSecret, redirectURI, refreshToken string, includeSigningGroups bool) (*Connector, error) {
	l := ctxzap.Extract(ctx)

	docusignClient, err := client.New(ctx, isDemo, clientId, clientSecret, redirectURI, refreshToken)
	if err != nil {
		l.Error("error creating DocuSign client", zap.Error(err))
		return nil, err
	}

	return &Connector{
		client:               docusignClient,
		includeSigningGroups: includeSigningGroups,
	}, nil
}

func NewWithClient(client *client.Client, includeSigningGroups bool) (*Connector, error) {
	return &Connector{
		client:               client,
		includeSigningGroups: includeSigningGroups,
	}, nil
}

func NewWithTokenSource(ctx context.Context, isDemo bool, tokenSource oauth2.TokenSource, includeSigningGroups bool) (*Connector, error) {
	docusignClient := client.NewClient(ctx, isDemo, tokenSource)

	return &Connector{
		client:               docusignClient,
		includeSigningGroups: includeSigningGroups,
	}, nil
}

func New(ctx context.Context, docusignCfg *cfg.Docusign, opts *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	l := ctxzap.Extract(ctx)
	var cb *Connector

	// Validate the configuration
	if err := field.Validate(cfg.ConfigurationSchema, docusignCfg); err != nil {
		return nil, nil, err
	}

	if opts.TokenSource != nil {
		cbWithTokenSource, err := NewWithTokenSource(ctx, true, opts.TokenSource, docusignCfg.IncludeSigningGroups)
		if err != nil {
			l.Error("error creating connector with token source", zap.Error(err))
			return nil, nil, err
		}

		cb = cbWithTokenSource
	} else {
		// In production, `docusignCfg.Configure` is always false.
		if docusignCfg.Configure {
			if err := Configure(ctx, docusignCfg); err != nil {
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("configuration complete")
		}

		if docusignCfg.RefreshToken == "" {
			return nil, nil, fmt.Errorf("refresh token is required, get it by running the connector with the --configure flag")
		}

		cbWithRefreshToken, err := NewWithRefreshToken(
			ctx,
			docusignCfg.Demo,
			docusignCfg.DocusignClientId,
			docusignCfg.DocusignClientSecret,
			docusignCfg.RedirectUri,
			docusignCfg.RefreshToken,
			docusignCfg.IncludeSigningGroups,
		)
		if err != nil {
			l.Error("error creating connector", zap.Error(err))
			return nil, nil, err
		}

		cb = cbWithRefreshToken
	}

	if cb == nil {
		return nil, nil, fmt.Errorf("connector initialization failed")
	}

	return cb, nil, nil
}
