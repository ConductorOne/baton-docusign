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

// Define the parent resource and pagination token used across integration tests.
var (
	parentResourceID = &v2.ResourceId{}
	pToken           = &pagination.Token{Size: 50, Token: "page-1"}
	pageToken        = &pagination.Token{Size: 50}
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

	client, err := client.New(ctx, apiURL, accountID, clientID, clientSecret, redirectURI, "")
	if err != nil {
		t.Fatalf("Failed to create DocuSign client: %v", err)
	}
	return client
}

// TestUserBuilderList verifies that users can be listed successfully from the DocuSign API.
func TestUserBuilderList(t *testing.T) {
	ctx := context.Background()
	client := initClient(t)

	user := newUserBuilder(client)
	resource, nextToken, _, err := user.List(ctx, parentResourceID, pToken)

	assert.NoError(t, err)
	assert.NotNil(t, resource)

	t.Logf("Users retrieved: %d, next token: %v", len(resource), nextToken)
}

// TestGroupBuilderList verifies that groups can be listed successfully from the DocuSign API.
func TestGroupBuilderList(t *testing.T) {
	ctx := context.Background()
	client := initClient(t)

	group := newGroupBuilder(client)
	resource, nextToken, _, err := group.List(ctx, parentResourceID, pToken)

	assert.NoError(t, err)
	assert.NotNil(t, resource)

	t.Logf("Groups retrieved: %d, next token: %v", len(resource), nextToken)
}

// TestPermissionBuilderList verifies that permission profiles can be listed successfully from the DocuSign API.
func TestPermissionBuilderList(t *testing.T) {
	ctx := context.Background()
	client := initClient(t)

	permission := newPermissionBuilder(client)
	resource, nextToken, _, err := permission.List(ctx, parentResourceID, pToken)

	assert.NoError(t, err)
	assert.NotNil(t, resource)

	t.Logf("Permission resources retrieved: %d, next token: %v", len(resource), nextToken)
}

// TestPermissionBuilderGrants verifies that grants can be retrieved for a user based on permission profiles.
// Skips the test if no users are available.
func TestPermissionBuilderGrants(t *testing.T) {
	ctx := context.Background()
	client := initClient(t)

	permission := newPermissionBuilder(client)
	user := newUserBuilder(client)

	users, _, _, err := user.List(ctx, parentResourceID, pToken)
	assert.NoError(t, err)

	if len(users) == 0 {
		t.Skip("No users available to test grants.")
	}

	grants, nextToken, _, err := permission.Grants(ctx, users[0], pToken)

	assert.NoError(t, err)
	assert.NotNil(t, grants)

	t.Logf("Grants retrieved: %d, next token: %v", len(grants), nextToken)
}
