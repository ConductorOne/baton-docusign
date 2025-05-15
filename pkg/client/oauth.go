package client

import (
	"context"
	"time"

	"golang.org/x/oauth2"
)

var (
	authURL      = "https://account-d.docusign.com/oauth/auth"
	tokenURL     = "https://account-d.docusign.com/oauth/token" //nolint:gosec // token URL does not contain sensitive credentials.
	defaultScope = "signature"
)

// OAuth2Docusign manages the OAuth2 configuration and token lifecycle for DocuSign.
type OAuth2Docusign struct {
	config      *oauth2.Config
	tokenSource oauth2.TokenSource
	token       *oauth2.Token
}

// getTokenSource creates a TokenSource that always refreshes using the provided refreshToken.
func getTokenSource(ctx context.Context, clientID, clientSecret, redirectURI, refreshToken string) oauth2.TokenSource {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       []string{defaultScope},
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}
	tok := &oauth2.Token{
		AccessToken:  "",
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-time.Second),
	}
	return oauth2.ReuseTokenSource(tok, cfg.TokenSource(ctx, tok))
}

// NewOAuth2Docusign initializes a new OAuth2Docusign helper with client credentials.
func NewOAuth2Docusign(clientID, clientSecret, redirectURI string) *OAuth2Docusign {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       []string{defaultScope},
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}
	// Start with no refresh token; will trigger initial authenticate flow.
	ts := getTokenSource(context.Background(), clientID, clientSecret, redirectURI, "")
	return &OAuth2Docusign{
		config:      cfg,
		tokenSource: ts,
		token:       nil,
	}
}
