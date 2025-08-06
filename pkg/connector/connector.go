package connector

import (
	"context"
	"io"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

type Connector struct {
	client            *client.Client
	skipSigningGroups bool
}

func (d *Connector) ResourceSyncers(_ context.Context) []connectorbuilder.ResourceSyncer {
	syncers := []connectorbuilder.ResourceSyncer{
		newUserBuilder(d.client),
		newGroupBuilder(d.client),
		newPermissionProfilesBuilder(d.client),
	}

	// Only include signing groups if not skipped
	if !d.skipSigningGroups {
		syncers = append(syncers, newSigningGroupBuilder(d.client))
	}

	return syncers
}

func (d *Connector) Asset(_ context.Context, _ *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

func (d *Connector) Metadata(_ context.Context) (*v2.ConnectorMetadata, error) {
	description := "Connector syncs data from Users, Permission Profiles, and Groups. It also allows the creation of users in DocuSign"
	if !d.skipSigningGroups {
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

func New(ctx context.Context, isDemo bool, clientId, clientSecret, redirectURI, refreshToken string, skipSigningGroups bool) (*Connector, error) {
	l := ctxzap.Extract(ctx)

	docusignClient, err := client.New(ctx, isDemo, clientId, clientSecret, redirectURI, refreshToken)
	if err != nil {
		l.Error("error creating DocuSign client", zap.Error(err))
		return nil, err
	}

	return &Connector{
		client:            docusignClient,
		skipSigningGroups: skipSigningGroups,
	}, nil
}

func NewWithClient(client *client.Client) (*Connector, error) {
	return &Connector{
		client: client,
	}, nil
}
