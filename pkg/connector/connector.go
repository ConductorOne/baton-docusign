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
	client *client.Client
	// includeClm gates whether the CLM builders' List() bodies do any work (see
	// ResourceSyncers) and which OAuth scopes are requested (see oauth.go); it does
	// not gate resource-type registration.
	includeClm bool
	// includeSigningGroups gates whether signingGroupBuilder.List() does any work —
	// same runtime-gate pattern as includeClm, not a registration gate either.
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
	oauth2Helper := client.NewOAuth2Docusign(docusignCfg.Demo, docusignCfg.DocusignClientId, docusignCfg.DocusignClientSecret, docusignCfg.RedirectUri, docusignCfg.IncludeClm)

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

// ResourceSyncers registers every resource type this connector can ever sync,
// unconditionally — including the opt-in ones (signing_group and the 5 CLM types).
//
// This is deliberate: previously, signing_group and the CLM types were only appended
// here when includeSigningGroups/includeClm were set, which meant flipping either flag
// off on a later sync makes ListResourceTypes() advertise fewer types than a prior
// sync did — C1 would then see zero resources of that type and could bucket every
// previously-synced resource and grant of it as deleted. Registering unconditionally
// and relying on &v2.OptInRequired{} (see resource_types.go) as the gate instead avoids
// that.
//
// includeClm and includeSigningGroups are both passed to their respective builders as
// runtime gates on whether List() does any work at all, not just whether the type is
// registered. Without that, every builder's List() would run on every sync for every
// account: for CLM, the first thing each of the 5 builders does is resolve the CLM
// base URL via an unconfirmed discovery call (see clm_client.go's package doc); for
// signing_group, GetSigningGroups hits an endpoint that may not be enabled on the
// account. isOptInFeatureUnavailableError only tolerates a 401/403 from either — any
// other failure (404, 5xx, a response schema matching none of the candidate fields, a
// transport error) would fail the whole sync for every account that never opted in.
// Gating on the flag before any client call avoids that entirely for the default
// (opted-out) case; the isOptInFeatureUnavailableError tolerance still matters for
// accounts that DID opt in but genuinely can't use the feature. includeClm also
// controls which OAuth scopes get requested (see oauth.go).
func (d *Connector) ResourceSyncers(_ context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{
		newUserBuilder(d.client),
		newGroupBuilder(d.client),
		newPermissionProfilesBuilder(d.client),
		newSigningGroupBuilder(d.client, d.includeSigningGroups),
		newClmMemberBuilder(d.client, d.includeClm),
		newClmRoleBuilder(d.client, d.includeClm),
		newClmGroupBuilder(d.client, d.includeClm),
		newClmPermissionSetBuilder(d.client, d.includeClm),
		newClmFolderBuilder(d.client, d.includeClm),
	}
}

func (d *Connector) Asset(_ context.Context, _ *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

func (d *Connector) Metadata(_ context.Context) (*v2.ConnectorMetadata, error) {
	// Signing groups and CLM are always registered as resource types (see
	// ResourceSyncers) and are gated by &v2.OptInRequired{} rather than this
	// description, so the description no longer branches on includeSigningGroups/
	// includeClm — it always lists everything the connector can sync.
	description := "Connector syncs data from Users, Permission Profiles, Groups, and Signing Groups (if enabled on your account). " +
		"Also syncs DocuSign CLM members, roles, groups, folders, folder security, and permission sets (if your account has a CLM subscription). " +
		"It also allows the creation of users in DocuSign"

	return &v2.ConnectorMetadata{
		DisplayName: "DocuSign",
		Description: description,
		AccountCreationSchema: &v2.ConnectorAccountCreationSchema{
			FieldMap: map[string]*v2.ConnectorAccountCreationSchema_Field{
				profileFieldEmail: {
					DisplayName: "Email",
					Required:    true,
					Description: "This email will be used as the login for the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Email",
					Order:       1,
				},
				profileFieldUsername: {
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

func NewWithRefreshToken(
	ctx context.Context, isDemo bool, clientId, clientSecret, redirectURI, refreshToken, accountId string,
	includeSigningGroups, includeClm bool, clmBaseURLOverride, baseURLOverride string,
) (*Connector, error) {
	l := ctxzap.Extract(ctx)

	docusignClient, err := client.New(
		ctx, isDemo, clientId, clientSecret, redirectURI, refreshToken, accountId,
		includeClm, clmBaseURLOverride, baseURLOverride,
	)
	if err != nil {
		l.Error("error creating DocuSign client", zap.Error(err))
		return nil, err
	}

	return &Connector{
		client:               docusignClient,
		includeClm:           includeClm,
		includeSigningGroups: includeSigningGroups,
	}, nil
}

func NewWithClient(client *client.Client, includeSigningGroups, includeClm bool) (*Connector, error) {
	return &Connector{
		client:               client,
		includeClm:           includeClm,
		includeSigningGroups: includeSigningGroups,
	}, nil
}

func NewWithTokenSource(
	ctx context.Context, isDemo bool, tokenSource oauth2.TokenSource, accountId string,
	includeSigningGroups, includeClm bool, clmBaseURLOverride string,
) (*Connector, error) {
	docusignClient := client.NewClient(ctx, isDemo, tokenSource, accountId, clmBaseURLOverride)

	return &Connector{
		client:               docusignClient,
		includeSigningGroups: includeSigningGroups,
		includeClm:           includeClm,
	}, nil
}

func New(ctx context.Context, docusignCfg *cfg.Docusign, opts *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	l := ctxzap.Extract(ctx)
	var cb *Connector

	// Validate the configuration
	if err := field.Validate(cfg.ConfigurationSchema, docusignCfg); err != nil {
		return nil, nil, err
	}

	// isDemo is set explicitly via --demo flag (CLI) or implied when a custom
	// client ID is provided (GUI demo group selection).
	isDemo := docusignCfg.Demo || opts.SelectedAuthMethod == "demo"

	if opts.TokenSource != nil {
		cbWithTokenSource, err := NewWithTokenSource(
			ctx, isDemo, opts.TokenSource, docusignCfg.AccountId,
			docusignCfg.IncludeSigningGroups, docusignCfg.IncludeClm, docusignCfg.ClmBaseUrl,
		)
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
			isDemo,
			docusignCfg.DocusignClientId,
			docusignCfg.DocusignClientSecret,
			docusignCfg.RedirectUri,
			docusignCfg.RefreshToken,
			docusignCfg.AccountId,
			docusignCfg.IncludeSigningGroups,
			docusignCfg.IncludeClm,
			docusignCfg.ClmBaseUrl,
			docusignCfg.BaseUrl,
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
