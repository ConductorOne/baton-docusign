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
	// includeSigningGroups gates whether the signing_group resource type is registered
	// at all (see ResourceSyncers). Unlike the CLM types, which are always registered,
	// this means ListResourceTypes() advertises a different set depending on the flag.
	includeSigningGroups bool
	// includeClm reports whether this sync will touch any CLM resource type — the same
	// opts.WillSyncResourceType(...) signal that already determines whether any CLM
	// builder's List() gets invoked this run (see New()). Gates Validate()'s upfront CLM
	// readiness check only: it does NOT gate resource-type registration. ResourceSyncers
	// always registers all 5 CLM builders unconditionally, because toggling registration
	// itself would make ListResourceTypes() advertise a different set between syncs and
	// C1 would read previously-synced CLM resources/grants as deleted.
	includeClm bool
	// skipPermissionProfileResourceType reports whether permission_profile is
	// excluded from the sync filter.
	skipPermissionProfileResourceType bool
}

// Configure handles the OAuth2 authorization flow to obtain a refresh token.
func Configure(ctx context.Context, docusignCfg *cfg.Docusign, includeClm bool) error {
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
	oauth2Helper := client.NewOAuth2Docusign(docusignCfg.Demo, docusignCfg.DocusignClientId, docusignCfg.DocusignClientSecret, docusignCfg.RedirectUri, includeClm)

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
		newUserBuilder(d.client, d.skipPermissionProfileResourceType),
		newGroupBuilder(d.client),
		newPermissionProfilesBuilder(d.client),
		newClmMemberBuilder(d.client),
		newClmRoleBuilder(),
		newClmGroupBuilder(d.client),
		newClmPermissionSetBuilder(d.client),
		newClmFolderBuilder(d.client),
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

// Validate runs once, before any resource type's List() (see baton-sdk's
// pkg/sync/syncer.go Sync()), so it's the right place to check readiness a single time
// upfront rather than discovering a bad account mid-sync at whichever builder's List()
// happens to run first. EnsureReady (base eSignature credentials) runs unconditionally
// — every sync needs those regardless of CLM — while EnsureClmReady is gated on
// includeClm: an account that never opted into any CLM resource type has no reason to
// pay for, or fail on, a CLM discovery call it doesn't need. This gate is separate from
// resource-type registration (see this file's includeClm field doc) and replaces each
// CLM builder's own List() checking readiness independently.
//
// # Known limitation, reviewed and accepted (not an open finding)
//
// includeClm is opts.WillSyncResourceType(...), sourced solely from the local
// --sync-resource-types/BATON_SYNC_RESOURCE_TYPES flag (baton-sdk pkg/cli/commands.go).
// ConductorOne's real per-task CLM opt-in is delivered on a separate path —
// pkg/tasks/c1api/full_sync.go's Task_SyncFullTask.GetSyncFull().GetSyncResourceTypeIds()
// feeds the syncer directly (pkg/sync/syncer.go) and never reaches ConnectorOpts. With
// no local flag set, includeClm is true on every ConductorOne-hosted and self-hosted
// service-mode run, regardless of whether that account opted into any clm_* type — so
// an eSignature-only account fails this entire sync below via EnsureClmReady, not just
// CLM. This has been raised and investigated multiple times (PR #63/#64 review history)
// with the same file:line evidence chain each time; the finding is accurate every time
// it resurfaces, but the fix (reverting to each CLM builder's own List()-time check, the
// design this replaced) was deliberately not taken: DocuSign authenticates via OAuth
// only, which makes the one deployment mode this gap actually affects — self-hosted /
// CLI service mode — assessed as impractical for real DocuSign customers. Re-flagging
// this exact gap is not new information; revisiting the tradeoff itself would be.
func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	if err := d.client.EnsureReady(ctx); err != nil {
		return nil, fmt.Errorf("baton-docusign: eSignature credential check failed: %w", err)
	}
	if !d.includeClm {
		return nil, nil
	}
	if err := d.client.EnsureClmReady(ctx); err != nil {
		return nil, fmt.Errorf("baton-docusign: CLM readiness check failed — clm_* resource types "+
			"are enabled for this sync but this account/credential cannot reach the CLM API; "+
			"disable those resource types or enable CLM on the account: %w", err)
	}
	return nil, nil
}

func NewWithRefreshToken(
	ctx context.Context, isDemo bool, clientId, clientSecret, redirectURI, refreshToken, accountId string,
	includeSigningGroups, includeClm bool, clmBaseURLOverride, baseURLOverride string,
	skipPermissionProfileResourceType bool,
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
		client:                            docusignClient,
		includeSigningGroups:              includeSigningGroups,
		includeClm:                        includeClm,
		skipPermissionProfileResourceType: skipPermissionProfileResourceType,
	}, nil
}

// NewWithTokenSource's token source is minted by ConductorOne's OAuth flow, so this
// path can't influence which scopes were granted (unlike NewWithRefreshToken, where
// includeClm also drives buildScopes) — but it still needs includeClm to gate
// Validate()'s CLM readiness check, so it's threaded through for that purpose alone.
func NewWithTokenSource(
	ctx context.Context, isDemo bool, tokenSource oauth2.TokenSource, accountId string,
	includeSigningGroups, includeClm bool, clmBaseURLOverride string,
	skipPermissionProfileResourceType bool,
) (*Connector, error) {
	docusignClient := client.NewClient(ctx, isDemo, tokenSource, accountId, clmBaseURLOverride)

	return &Connector{
		client:                            docusignClient,
		includeSigningGroups:              includeSigningGroups,
		includeClm:                        includeClm,
		skipPermissionProfileResourceType: skipPermissionProfileResourceType,
	}, nil
}

func New(ctx context.Context, docusignCfg *cfg.Docusign, opts *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	l := ctxzap.Extract(ctx)
	var cb *Connector

	includeClm := opts.WillSyncResourceType(clmMemberResourceType.Id) || opts.WillSyncResourceType(clmRoleResourceType.Id) ||
		opts.WillSyncResourceType(clmGroupResourceType.Id) || opts.WillSyncResourceType(clmPermissionSetResourceType.Id) ||
		opts.WillSyncResourceType(clmFolderResourceType.Id)

	// Validate the configuration
	if err := field.Validate(cfg.ConfigurationSchema, docusignCfg); err != nil {
		return nil, nil, err
	}

	// isDemo is set explicitly via --demo flag (CLI) or implied when a custom
	// client ID is provided (GUI demo group selection).
	isDemo := docusignCfg.Demo || opts.SelectedAuthMethod == "demo"

	// nil opts means no filter, so nothing is skipped.
	skipPermissionProfileResourceType := opts != nil && !opts.WillSyncResourceType(PermissionProfileResourceTypeID)

	if opts.TokenSource != nil {
		cbWithTokenSource, err := NewWithTokenSource(
			ctx, isDemo, opts.TokenSource, docusignCfg.AccountId,
			docusignCfg.IncludeSigningGroups, includeClm, docusignCfg.ClmBaseUrl,
			skipPermissionProfileResourceType,
		)
		if err != nil {
			l.Error("error creating connector with token source", zap.Error(err))
			return nil, nil, err
		}

		cb = cbWithTokenSource
	} else {
		// In production, `docusignCfg.Configure` is always false.
		if docusignCfg.Configure {
			if err := Configure(ctx, docusignCfg, includeClm); err != nil {
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
			includeClm,
			docusignCfg.ClmBaseUrl,
			docusignCfg.BaseUrl,
			skipPermissionProfileResourceType,
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
