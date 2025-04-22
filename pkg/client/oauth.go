package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	authURL      = "https://account-d.docusign.com/oauth/auth"  // URL for OAuth2 authorization
	tokenURL     = "https://account-d.docusign.com/oauth/token" // URL for OAuth2 token exchange
	defaultScope = "signature"                                  // Default OAuth2 scope
)

type customTokenSource struct {
	ctx          context.Context // Context for OAuth2 operations
	oauthConfig  *oauth2.Config  // OAuth2 configuration
	currentToken *oauth2.Token   // Current OAuth2 token
	clientID     string          // OAuth2 client ID
	clientSecret string          // OAuth2 client secret
	accountID    string          // Account ID for DocuSign
	redirectUri  string          // Redirect URI for OAuth2 authorization
	mu           sync.Mutex      // Mutex to synchronize access to token
}

// newOAuth2Config creates and returns a new OAuth2 configuration with the provided client ID, client secret, and redirect URI.
func newOAuth2Config(clientID, clientSecret, redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       []string{defaultScope},
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}
}

// Token retrieves the OAuth2 token. It checks if a valid token is available, tries to refresh it if needed,
// or generates a new token if none is found or the current token is expired.
func (ts *customTokenSource) Token() (*oauth2.Token, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.currentToken != nil && ts.currentToken.Valid() {
		return ts.currentToken, nil
	}

	savedToken, err := loadTokenFromFile()
	if err == nil && savedToken != nil && savedToken.Valid() {
		ts.currentToken = savedToken
		return savedToken, nil
	}

	if ts.currentToken != nil && ts.currentToken.RefreshToken != "" {
		tok, err := ts.oauthConfig.TokenSource(ts.ctx, ts.currentToken).Token()
		if err == nil {
			ts.saveNewToken(tok)
			return tok, nil
		}
	}

	if savedToken != nil && savedToken.RefreshToken != "" {
		tok, err := ts.oauthConfig.TokenSource(ts.ctx, savedToken).Token()
		if err == nil {
			ts.saveNewToken(tok) 
			return tok, nil
		}
	}

	return ts.generateInitialToken()
}

// generateInitialToken generates a new OAuth2 token by prompting the user to authorize the application
// and exchange the authorization code for a token.
func (ts *customTokenSource) generateInitialToken() (*oauth2.Token, error) {
	authURL := ts.oauthConfig.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following URL to authorize the application:\n\n%s\n\n", authURL)
	fmt.Printf("After authorization, you will be redirected to %s. Paste the full redirect URL here:\n", ts.redirectUri)

	var redirectURL string
	if _, err := fmt.Scanln(&redirectURL); err != nil {
		return nil, fmt.Errorf("failed to read redirect URL: %v", err)
	}

	u, err := url.Parse(redirectURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redirect URL: %v", err)
	}

	code := u.Query().Get("code")
	if code == "" {
		return nil, fmt.Errorf("authorization code not found in redirect URL")
	}

	token, err := ts.exchangeCodeForToken(code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for token: %v", err)
	}

	ts.saveNewToken(token)
	return token, nil
}

// exchangeCodeForToken exchanges the authorization code for an OAuth2 token
// by making a POST request to the token endpoint.
func (ts *customTokenSource) exchangeCodeForToken(code string) (*oauth2.Token, error) {
	// Prepare the authorization header with the client ID and secret
	authHeader := base64.StdEncoding.EncodeToString(
		[]byte(ts.clientID + ":" + ts.clientSecret),
	)

	// Prepare the form data for the token request
	form := url.Values{}
	form.Add("grant_type", "authorization_code")
	form.Add("code", code)
	form.Add("redirect_uri", ts.redirectUri)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %v", err)
	}

	// Add the necessary headers for the request
	req.Header.Add("Authorization", "Basic "+authHeader)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute token request: %v", err)
	}
	defer resp.Body.Close()

	// If the response status is not OK, handle the error
	if resp.StatusCode != http.StatusOK {
		var errorResponse struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errorResponse)
		return nil, fmt.Errorf("token request failed with status %d: %s - %s",
			resp.StatusCode, errorResponse.Error, errorResponse.ErrorDescription)
	}

	// Decode the token from the response
	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %v", err)
	}

	return &token, nil
}

// saveNewToken saves the newly obtained OAuth2 token and writes it to a file for future use.
func (ts *customTokenSource) saveNewToken(token *oauth2.Token) {
	ts.currentToken = token
	if err := saveTokenToFile(token); err != nil {
		log.Printf("Error saving token: %v", err)
	}
	log.Printf("New token obtained. Expires in %v\n", time.Until(token.Expiry))
}

// NewAuthenticatedClient creates a new authenticated HTTP client using OAuth2 authentication.
// It returns the HTTP client, the OAuth2 token, and any errors encountered during the process.
func NewAuthenticatedClient(ctx context.Context, clientID, clientSecret, accountID, redirectURI string) (*http.Client, *oauth2.Token, error) {
	oauthCfg := newOAuth2Config(clientID, clientSecret, redirectURI)

	ts := &customTokenSource{
		ctx:          ctx,
		oauthConfig:  oauthCfg,
		clientID:     clientID,
		clientSecret: clientSecret,
		accountID:    accountID,
		redirectUri:  redirectURI,
	}

	savedToken, err := loadTokenFromFile()
	if err == nil {
		ts.currentToken = savedToken
	}

	httpClient := oauth2.NewClient(ctx, ts)
	tok, err := ts.Token()
	if err != nil {
		return nil, nil, err
	}

	return httpClient, tok, nil
}
