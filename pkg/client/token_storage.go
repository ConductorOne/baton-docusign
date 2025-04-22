package client

import (
	"encoding/json"
	"os"
	"time"

	"golang.org/x/oauth2"
)

// tokenFileData is a structure that represents the data stored in the token file,
// which includes the access token, refresh token, and the token expiry time.
type tokenFileData struct {
	AccessToken  string    `json:"access_token"`  // OAuth2 access token
	RefreshToken string    `json:"refresh_token"` // OAuth2 refresh token
	Expiry       time.Time `json:"expiry"`        // Token expiration time
}

const tokenFilePath = "docusign_token.json"

// loadTokenFromFile loads the OAuth2 token from a file. It reads the saved token data
// from the `docusign_token.json` file, unmarshals it into a `tokenFileData` structure,
// and returns an `oauth2.Token` object.
func loadTokenFromFile() (*oauth2.Token, error) {
	data, err := os.ReadFile(tokenFilePath)
	if err != nil {
		return nil, err
	}


	var saved tokenFileData
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err 
	}

	return &oauth2.Token{
		AccessToken:  saved.AccessToken,
		RefreshToken: saved.RefreshToken,
		Expiry:       saved.Expiry,
		TokenType:    "Bearer",
	}, nil
}

// saveTokenToFile saves the provided OAuth2 token to a file. It serializes the token's
// access token, refresh token, and expiry time into JSON format and writes it to the
// `docusign_token.json` file.
func saveTokenToFile(token *oauth2.Token) error {
	data := tokenFileData{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err 
	}


	return os.WriteFile(tokenFilePath, jsonData, 0600)
}
