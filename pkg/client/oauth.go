package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

var (
	authURL      = "https://account-d.docusign.com/oauth/auth"
	tokenURL     = "https://account-d.docusign.com/oauth/token" //nolint:gosec // Not a token, it's an endpoint URL
	defaultScope = "signature"
	test         = "https://account-d.docusign.com/oauth/userinfo"
)

type customTokenSource struct {
	ctx          context.Context
	oauthConfig  *oauth2.Config
	currentToken *oauth2.Token
	clientID     string
	clientSecret string
	accountID    string
	redirectURI  string
	mu           sync.Mutex
}

type retryingTokenSource struct {
	baseSource oauth2.TokenSource
	mu         sync.Mutex
}

// newOAuth2Config creates a new OAuth2 configuration using the provided client credentials and redirect URI.
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

// Token attempts to retrieve a valid token from the underlying source.
// If the token is expired and unauthorized, it retries the request using a new token.
func (r *retryingTokenSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tok, err := r.baseSource.Token()
	if err != nil {
		return nil, err
	}

	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(tok))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, test, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode == http.StatusUnauthorized {
		r.baseSource = oauth2.ReuseTokenSource(nil, r.baseSource)
		return r.baseSource.Token()
	}
	defer resp.Body.Close()

	return tok, nil
}

// Token returns a valid access token, either by using the current one,
// refreshing it using a refresh token, or by initiating the authorization code flow.
func (ts *customTokenSource) Token() (*oauth2.Token, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.currentToken != nil && ts.currentToken.Valid() {
		return ts.currentToken, nil
	}

	savedToken, err := loadTokenFromFile()
	if err == nil && savedToken.RefreshToken != "" {
		newToken, err := ts.refreshToken(savedToken.RefreshToken)
		if err == nil {
			return newToken, nil
		}
	}

	return ts.generateInitialToken()
}

// refreshToken refreshes the access token using the provided refresh token and returns the new token.
func (ts *customTokenSource) refreshToken(refreshToken string) (*oauth2.Token, error) {
	authHeader := base64.StdEncoding.EncodeToString([]byte(ts.clientID + ":" + ts.clientSecret))
	form := url.Values{}
	form.Add("grant_type", "refresh_token")
	form.Add("refresh_token", refreshToken)
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Add("Authorization", "Basic "+authHeader)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	token := &oauth2.Token{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		RefreshToken: tokenResp.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	ts.saveNewToken(token)
	return token, nil
}

// generateInitialToken guides the user through the OAuth2 authorization code flow,
// obtains the code, and exchanges it for a new token.
func (ts *customTokenSource) generateInitialToken() (*oauth2.Token, error) {
	authURL := ts.oauthConfig.AuthCodeURL("state-token", oauth2.AccessTypeOffline)

	log.Printf("Go to the following URL to authorize the application:\n\n%s\n\n", authURL)
	log.Printf("After authorization, you will be redirected to %s. Paste the full redirect URL here:\n", ts.redirectURI)

	var redirectURL string
	if _, err := fmt.Scanln(&redirectURL); err != nil {
		return nil, fmt.Errorf("failed to read redirect URL: %w", err)
	}

	u, err := url.Parse(redirectURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
	}

	code := u.Query().Get("code")
	if code == "" {
		return nil, fmt.Errorf("authorization code not found in redirect URL")
	}

	token, err := ts.exchangeCodeForToken(code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for token: %w", err)
	}

	ts.saveNewToken(token)
	return token, nil
}

// exchangeCodeForToken exchanges the provided authorization code for a new OAuth2 token.
func (ts *customTokenSource) exchangeCodeForToken(code string) (*oauth2.Token, error) {
	authHeader := base64.StdEncoding.EncodeToString([]byte(ts.clientID + ":" + ts.clientSecret))
	form := url.Values{}
	form.Add("grant_type", "authorization_code")
	form.Add("code", code)
	form.Add("redirect_uri", ts.redirectURI)

	// Crear el contexto. Si no tienes uno específico, puedes usar context.Background().
	ctx := context.Background()

	// Usar http.NewRequestWithContext en lugar de http.NewRequest
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Add("Authorization", "Basic "+authHeader)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	token := &oauth2.Token{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		RefreshToken: tokenResp.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}
	ts.saveNewToken(token)
	return token, nil
}

// saveNewToken saves the new token in memory and persists it to file.
func (ts *customTokenSource) saveNewToken(token *oauth2.Token) {
	ts.currentToken = token
	_ = saveTokenToFile(token)
}

// NewAuthenticatedClient initializes an HTTP client authenticated with OAuth2,
// handling token loading, refreshing, and reuse.
func NewAuthenticatedClient(ctx context.Context, clientID, clientSecret, accountID, redirectURI string) (*http.Client, *oauth2.Token, error) {
	oauthCfg := newOAuth2Config(clientID, clientSecret, redirectURI)

	ts := &customTokenSource{
		ctx:          ctx,
		oauthConfig:  oauthCfg,
		clientID:     clientID,
		clientSecret: clientSecret,
		accountID:    accountID,
		redirectURI:  redirectURI,
	}

	savedToken, err := loadTokenFromFile()
	if err == nil {
		ts.currentToken = savedToken
	}

	reusableSource := oauth2.ReuseTokenSource(savedToken, ts)
	retryingSource := &retryingTokenSource{baseSource: reusableSource}
	httpClient := oauth2.NewClient(ctx, retryingSource)
	tok, err := ts.Token()
	if err != nil {
		return nil, nil, err
	}

	return httpClient, tok, nil
}
