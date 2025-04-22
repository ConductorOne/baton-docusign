package client

import (
	"encoding/json"
	"os"
	"time"

	"golang.org/x/oauth2"
)

type tokenFileData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

const tokenFilePath = "docusign_token.json"

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
