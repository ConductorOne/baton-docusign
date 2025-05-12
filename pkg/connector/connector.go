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
	client *client.Client
}

func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		newUserBuilder(d.client),
		newGroupBuilder(d.client),
		newPermissionBuilder(d.client),
	}
}

func (d *Connector) Asset(ctx context.Context, asset *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

func (d *Connector) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "DocuSign",
		Description: "Connector syncs data users, permissions, groups  and create users of DocuSign",
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

func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	return nil, nil
}

func New(ctx context.Context, apiUrl, accountId, clientId, clientSecret, redirectURI, refreshToken string) (*Connector, error) {
	l := ctxzap.Extract(ctx)

	docusignClient, err := client.New(ctx, apiUrl, accountId, clientId, clientSecret, redirectURI, refreshToken)
	if err != nil {
		l.Error("error creating DocuSign client", zap.Error(err))
		return nil, err
	}

	return &Connector{
		client: docusignClient,
	}, nil
}
