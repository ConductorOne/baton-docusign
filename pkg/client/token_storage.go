package client

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2"
)

// Struct used to store OAuth2 token information in a JSON file.
type tokenFileData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

const configFilePath = "docusign_token.json"

// loadTokenFromFile loads the OAuth2 token from a local JSON file.
// If the expiry is not present, it calculates it based on file creation time.
func loadTokenFromFile() (*oauth2.Token, error) {
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading token file: %w", err)
	}

	var saved tokenFileData
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("error decoding token file: %w", err)
	}

	// If the expiry field is missing, use file modification time + 8h
	if saved.Expiry.IsZero() {
		info, err := os.Stat(configFilePath)
		if err != nil {
			return nil, fmt.Errorf("unable to get token file creation date: %w", err)
		}
		saved.Expiry = info.ModTime().Add(8 * time.Hour)
	}

	token := &oauth2.Token{
		AccessToken:  saved.AccessToken,
		RefreshToken: saved.RefreshToken,
		TokenType:    saved.TokenType,
		Expiry:       saved.Expiry,
	}

	return token, nil
}

// saveTokenToFile saves the OAuth2 token to a local JSON file with restricted permissions.
// If expiry is missing, it defaults to 1 hour from current time.
func saveTokenToFile(token *oauth2.Token) error {
	if token.Expiry.IsZero() {
		token.Expiry = time.Now().Add(time.Hour)
	}

	data := tokenFileData{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
	}

	fileData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("error serializing token: %w", err)
	}

	if err := os.WriteFile(configFilePath, fileData, 0600); err != nil {
		return fmt.Errorf("error writing token file: %w", err)
	}

	return nil
}
