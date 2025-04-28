package connector

import (
	"context"
	"os"
	"testing"

	"github.com/conductorone/baton-docusign/pkg/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/stretchr/testify/assert"
)

var (
	// Define the parent resource and pagination token used across integration tests.
	parentResourceID = &v2.ResourceId{}
	pToken           = &pagination.Token{Size: 50, Token: ""}
)

// initClient initializes a DocuSign client using environment variables.
// Skips the tests if any required environment variable is missing.
func initClient(t *testing.T) *client.Client {
	ctx := context.Background()

	apiURL, apiOK := os.LookupEnv("DOCUSIGN_API_URL")
	accountID, accOK := os.LookupEnv("DOCUSIGN_ACCOUNT_ID")
	clientID, cliOK := os.LookupEnv("DOCUSIGN_CLIENT_ID")
	clientSecret, secOK := os.LookupEnv("DOCUSIGN_CLIENT_SECRET")
	redirectURI, redirOK := os.LookupEnv("DOCUSIGN_REDIRECT_URI")

	if !apiOK || !accOK || !cliOK || !secOK || !redirOK {
		t.Skip("One or more required environment variables are missing. Skipping integration test.")
	}

	c, err := client.New(ctx, apiURL, accountID, clientID, clientSecret, redirectURI)
	if err != nil {
		t.Fatalf("Failed to create DocuSign client: %v", err)
	}
	return c
}

// TestUserBuilderList verifies that users can be listed successfully from the DocuSign API.
func TestUserBuilderList(t *testing.T) {
	ctx := context.Background()
	c := initClient(t)

	u := newUserBuilder(c)
	res, nextToken, _, err := u.List(ctx, parentResourceID, pToken)

	assert.NoError(t, err)
	assert.NotNil(t, res)

	t.Logf("Users retrieved: %d, next token: %v", len(res), nextToken)
}

// TestGroupBuilderList verifies that groups can be listed successfully from the DocuSign API.
func TestGroupBuilderList(t *testing.T) {
	ctx := context.Background()
	c := initClient(t)

	g := newGroupBuilder(c)
	res, nextToken, _, err := g.List(ctx, parentResourceID, pToken)

	assert.NoError(t, err)
	assert.NotNil(t, res)

	t.Logf("Groups retrieved: %d, next token: %v", len(res), nextToken)
}

// TestPermissionBuilderList verifies that permission profiles can be listed successfully from the DocuSign API.
func TestPermissionBuilderList(t *testing.T) {
	ctx := context.Background()
	c := initClient(t)

	p := newPermissionBuilder(c)
	res, nextToken, _, err := p.List(ctx, parentResourceID, pToken)

	assert.NoError(t, err)
	assert.NotNil(t, res)

	t.Logf("Permission resources retrieved: %d, next token: %v", len(res), nextToken)
}

// TestPermissionBuilderGrants verifies that grants can be retrieved for a user based on permission profiles.
// Skips the test if no users are available.
func TestPermissionBuilderGrants(t *testing.T) {
	ctx := context.Background()
	c := initClient(t)

	p := newPermissionBuilder(c)
	u := newUserBuilder(c)

	users, _, _, err := u.List(ctx, parentResourceID, pToken)
	assert.NoError(t, err)

	if len(users) == 0 {
		t.Skip("No users available to test grants.")
	}

	grants, nextToken, _, err := p.Grants(ctx, users[0], pToken)

	assert.NoError(t, err)
	assert.NotNil(t, grants)

	t.Logf("Grants retrieved: %d, next token: %v", len(grants), nextToken)
}
