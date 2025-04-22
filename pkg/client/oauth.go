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
	authURL      = "https://account-d.docusign.com/oauth/auth"
	tokenURL     = "https://account-d.docusign.com/oauth/token"
	redirectURI  = "http://localhost:8080/callback"
	defaultScope = "signature"
)

type customTokenSource struct {
	ctx          context.Context
	oauthConfig  *oauth2.Config
	currentToken *oauth2.Token

	clientID     string
	clientSecret string
	accountID    string
	apiURL       string

	mu sync.Mutex
}

func newOAuth2Config(clientID, clientSecret string) *oauth2.Config {
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

	return ts.generateInitialToken()
}

func (ts *customTokenSource) generateInitialToken() (*oauth2.Token, error) {
	authURL := ts.oauthConfig.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following URL to authorize the application:\n\n%s\n\n", authURL)
	fmt.Printf("After authorization, you will be redirected to %s. Paste the full redirect URL here:\n", redirectURI)

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

func (ts *customTokenSource) exchangeCodeForToken(code string) (*oauth2.Token, error) {
	authHeader := base64.StdEncoding.EncodeToString([]byte(ts.clientID + ":" + ts.clientSecret))

	form := url.Values{}
	form.Add("grant_type", "authorization_code")
	form.Add("code", code)
	form.Add("redirect_uri", redirectURI)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", "Basic "+authHeader)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, err
	}

	return &token, nil
}

func (ts *customTokenSource) saveNewToken(token *oauth2.Token) {
	ts.currentToken = token
	if err := saveTokenToFile(token); err != nil {
		log.Printf("Error saving token: %v", err)
	}
	log.Printf("New token obtained. Expires in %v\n", time.Until(token.Expiry))
}

func NewAuthenticatedClient(ctx context.Context, clientID, clientSecret, accountID, apiURL string) (*http.Client, *oauth2.Token, error) {
	oauthCfg := newOAuth2Config(clientID, clientSecret)

	ts := &customTokenSource{
		ctx:          ctx,
		oauthConfig:  oauthCfg,
		clientID:     clientID,
		clientSecret: clientSecret,
		accountID:    accountID,
		apiURL:       apiURL,
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
