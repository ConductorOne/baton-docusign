package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"golang.org/x/oauth2"
)

var (
	authURL      = "https://account-d.docusign.com/oauth/auth"
	tokenURL     = "https://account-d.docusign.com/oauth/token" //nolint:gosec // token URL does not contain sensitive credentials
	defaultScope = "signature"
)

// OAuth2Docusign manages the OAuth2 configuration and token lifecycle for DocuSign.
type OAuth2Docusign struct {
	config      *oauth2.Config
	tokenSource oauth2.TokenSource
	token       *oauth2.Token
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
	return &OAuth2Docusign{config: cfg}
}

// SetAccessToken configures a static access token without refresh support.
func (o *OAuth2Docusign) SetAccessToken(accessToken string) {
	tok := &oauth2.Token{AccessToken: accessToken, TokenType: "Bearer"}
	o.token = tok
	o.tokenSource = oauth2.StaticTokenSource(tok)
}

// Authenticate launches the interactive OAuth2 flow if no token source exists.
func (o *OAuth2Docusign) Authenticate(ctx context.Context) error {
	if o.tokenSource != nil {
		return nil
	}

	authCodeURL := o.config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	log.Printf("Go to the following URL in your browser and authorize:\n%s\n", authCodeURL)
	log.Printf("After authorization, paste the full redirect URL here:")

	var redirectURL string
	log.Print("Paste the full redirect URL: ")
	_, err := fmt.Scanln(&redirectURL)
	if err != nil {
		return fmt.Errorf("read redirect URL: %w", err)
	}
	redirectURL = strings.TrimSpace(redirectURL)

	u, err := url.Parse(redirectURL)
	if err != nil {
		return fmt.Errorf("parse redirect URL: %w", err)
	}

	code := u.Query().Get("code")
	if code == "" {
		return errors.New("no authorization code found in URL")
	}

	tok, err := o.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}

	o.token = tok
	o.tokenSource = o.config.TokenSource(ctx, tok)
	return nil
}

// Client returns an HTTP client that automatically refreshes tokens as needed.
func (o *OAuth2Docusign) Client(ctx context.Context) (*uhttp.BaseHttpClient, error) {
	if err := o.Authenticate(ctx); err != nil {
		return nil, err
	}
	httpClient := oauth2.NewClient(ctx, o.tokenSource)
	return uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
}

// Token retrieves the last obtained OAuth2 token.
func (o *OAuth2Docusign) Token() *oauth2.Token {
	return o.token
}

// NewClientFromAccessToken creates a BaseHttpClient using a fixed access token (no refresh).
func NewClientFromAccessToken(ctx context.Context, accessToken string) (*uhttp.BaseHttpClient, error) {
	tok := &oauth2.Token{AccessToken: accessToken, TokenType: "Bearer"}
	ts := oauth2.StaticTokenSource(tok)
	httpClient := oauth2.NewClient(ctx, ts)
	return uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
}
