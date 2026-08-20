package client

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var (
	authURLDemo  = "https://account-d.docusign.com/oauth/auth"
	tokenURLDemo = "https://account-d.docusign.com/oauth/token" //nolint:gosec // token URL does not contain sensitive credentials.
	authURLProd  = "https://account.docusign.com/oauth/auth"
	tokenURLProd = "https://account.docusign.com/oauth/token" //nolint:gosec // token URL does not contain sensitive credentials.
	defaultScope = "signature"
	// clmScopes are the OAuth scopes required to call the DocuSign CLM API, per
	// DocuSign's own CLM Authentication Overview docs (pasted live, since the site is
	// JS-rendered and unreachable by automated tools): the "Required scopes" section's
	// Authorization Code Grant example is exactly signature+spring_read+spring_write,
	// with an explicit note that "impersonation" is a JWT-Grant-only requirement — NOT
	// needed here. An earlier version of this code added "impersonation" and
	// "springcm_read"/"springcm_write" based on a live 401 ("Access Denied", CLM
	// ErrorCode 103) against a real CLM tenant plus unreliable secondary docs sources;
	// that live tenant already had exactly this scope set granted when it failed, so
	// the 401 is not a scope problem — see clm_client.go's package doc for the current
	// working theory (demo/UAT environment limitation, per CLM's docs requiring a
	// production account).
	clmScopes = []string{"spring_read", "spring_write"}
)

// buildScopes returns the OAuth scopes to request, adding CLM scopes when includeClm is set.
func buildScopes(includeClm bool) []string {
	scopes := []string{defaultScope}
	if includeClm {
		scopes = append(scopes, clmScopes...)
	}
	return scopes
}

// OAuth2Docusign manages the OAuth2 configuration and token lifecycle for DocuSign.
type OAuth2Docusign struct {
	config      *oauth2.Config
	tokenSource oauth2.TokenSource
	token       *oauth2.Token
}

// getTokenSource creates a TokenSource that always refreshes using the provided refreshToken.
func getTokenSource(ctx context.Context, isDemo bool, clientID, clientSecret, redirectURI, refreshToken string, includeClm bool) oauth2.TokenSource {
	authURL := authURLProd
	tokenURL := tokenURLProd
	if isDemo {
		authURL = authURLDemo
		tokenURL = tokenURLDemo
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       buildScopes(includeClm),
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
func NewOAuth2Docusign(isDemo bool, clientID, clientSecret, redirectURI string, includeClm bool) *OAuth2Docusign {
	authURL := authURLProd
	tokenURL := tokenURLProd
	if isDemo {
		authURL = authURLDemo
		tokenURL = tokenURLDemo
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       buildScopes(includeClm),
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}
	// Start with no refresh token; will trigger initial authenticate flow.
	ts := getTokenSource(context.Background(), isDemo, clientID, clientSecret, redirectURI, "", includeClm)
	return &OAuth2Docusign{
		config:      cfg,
		tokenSource: ts,
		token:       nil,
	}
}

// Authorize initiates the OAuth2 authorization code flow and prompts the user to enter the authorization code.
func (o *OAuth2Docusign) Authorize(ctx context.Context) (string, error) {
	// Generate the authorization URL using oauth2.Config.AuthCodeURL
	// Note: For CLI flows, state parameter is less critical than web flows,
	// but we include it for consistency with OAuth2 best practices
	authURL := o.config.AuthCodeURL("state", oauth2.AccessTypeOffline)

	fmt.Fprintf(os.Stdout, "\nPlease visit the following URL to authorize the application:\n")
	fmt.Fprintf(os.Stdout, "\n%s\n\n", authURL)
	fmt.Fprint(os.Stdout, "Enter the authorization code: ")

	// Read the authorization code from user input
	reader := bufio.NewReader(os.Stdin)
	code, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read authorization code: %w", err)
	}

	// Trim whitespace and newline characters
	code = strings.TrimSpace(code)

	if code == "" {
		return "", fmt.Errorf("authorization code cannot be empty")
	}

	return code, nil
}

// ExchangeCodeForToken exchanges an authorization code for an access token and refresh token.
func (o *OAuth2Docusign) ExchangeCodeForToken(ctx context.Context, code string) (*oauth2.Token, error) {
	token, err := o.config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	o.token = token
	o.tokenSource = o.config.TokenSource(ctx, token)

	return token, nil
}
