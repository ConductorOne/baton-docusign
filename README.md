![Baton Logo](./baton-logo.png)

# `baton-docusign` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-docusign.svg)](https://pkg.go.dev/github.com/conductorone/baton-docusign) ![main ci](https://github.com/conductorone/baton-docusign/actions/workflows/main.yaml/badge.svg)

`baton-docusign` is a connector for DocuSign built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the DocuSign eSignature REST API v2.1 to sync users, groups, signing groups, and permission profiles.

Check out [Baton](https://github.com/conductorone/baton) to learn more about the project in general.

## Connector Capabilities

### Resources Synced

- Users
- Groups
- Signing Groups
- Permission Profiles

### Provisioning Support

- Users (create accounts)
- Group membership (grant/revoke)
- Signing group membership (grant/revoke)
- Permission profiles (grant only - users must always have a profile assigned)

## Connector Credentials

To connect to DocuSign, you will need the following credentials:

1. **Client ID** (Integration Key)
2. **Client Secret**
3. **Redirect URI**
4. **Refresh Token**
5. **Environment Selection** (demo or production)

### Obtaining Credentials

#### Step 1: Create OAuth Integration in DocuSign

1. Log in to [DocuSign Developer Account](https://account-d.docusign.com) (demo) or [DocuSign Production](https://account.docusign.com) (production)
2. Go to **Admin → Apps and Keys**
3. Click **Add App and Integration Key**
4. Configure the app:
   - Enter an app name (e.g., "Baton Connector")
   - Enable **User Application**
   - Click **Add Secret Key** and save the **Client Secret** securely
   - Under **Additional Settings**, add your **Redirect URI** (e.g., `http://example.com/callback`)
   - Under **CORS Settings** enable GET, POST, PUT, DELETE, and HEAD
5. Save the application and copy the **Integration Key** (Client ID)

#### Step 2: Obtain Refresh Token

The connector provides a convenient `--configure` flag to obtain your refresh token:

```bash
baton-docusign \
  --demo=true \
  --clientId "YOUR_CLIENT_ID" \
  --clientSecret "YOUR_CLIENT_SECRET" \
  --redirect-uri "http://example.com/callback" \
  --configure
```

This will:

1. Display an authorization URL
2. Prompt you to visit the URL and authorize the application
3. Ask you to paste the authorization code from the redirect URL
4. Exchange the code for a refresh token and display it

**Example output:**

```
Please visit the following URL to authorize the application:

https://account-d.docusign.com/oauth/auth?response_type=code&scope=signature&client_id=...

Enter the authorization code: <paste code here>

refresh token: eyJ0eXAiOiJNVCIsImFsZyI6...
```

After visiting the URL and authorizing, you'll be redirected to:

```
http://example.com/callback?code=AUTHORIZATION_CODE
```

Copy the `code` parameter value and paste it when prompted. Save the refresh token for future use.

# Getting Started

## Prerequisites

Before using the connector, ensure you have:

- DocuSign account (demo or production)
- Admin access to create OAuth integrations
- Client ID, Client Secret, and Redirect URI (see [Obtaining Credentials](#obtaining-credentials))

## brew

```bash
brew install conductorone/baton/baton conductorone/baton/baton-docusign

# First, obtain your refresh token
baton-docusign \
  --demo=true \
  --clientId "YOUR_CLIENT_ID" \
  --clientSecret "YOUR_CLIENT_SECRET" \
  --redirect-uri "YOUR_REDIRECT_URI" \
  --configure

# Then, run the connector with your refresh token
baton-docusign \
  --demo=true \
  --clientId "YOUR_CLIENT_ID" \
  --clientSecret "YOUR_CLIENT_SECRET" \
  --redirect-uri "YOUR_REDIRECT_URI" \
  --refresh-token "YOUR_REFRESH_TOKEN"

baton resources
```

## docker

```bash
# First, obtain your refresh token using --configure
docker run --rm -it \
  -e BATON_DEMO=true \
  -e BATON_CLIENTID=YOUR_CLIENT_ID \
  -e BATON_CLIENTSECRET=YOUR_CLIENT_SECRET \
  -e BATON_REDIRECT_URI=YOUR_REDIRECT_URI \
  ghcr.io/conductorone/baton-docusign:latest --configure

# Then, run the connector with your refresh token
docker run --rm -v $(pwd):/out \
  -e BATON_DEMO=true \
  -e BATON_CLIENTID=YOUR_CLIENT_ID \
  -e BATON_CLIENTSECRET=YOUR_CLIENT_SECRET \
  -e BATON_REDIRECT_URI=YOUR_REDIRECT_URI \
  -e BATON_REFRESH_TOKEN=YOUR_REFRESH_TOKEN \
  ghcr.io/conductorone/baton-docusign:latest -f "/out/sync.c1z"

docker run --rm -v $(pwd):/out \
  ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```bash
# Install baton and baton-docusign
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-docusign/cmd/baton-docusign@main

# First, obtain your refresh token
baton-docusign \
  --demo=true \
  --clientId "YOUR_CLIENT_ID" \
  --clientSecret "YOUR_CLIENT_SECRET" \
  --redirect-uri "YOUR_REDIRECT_URI" \
  --configure

# Then, run the connector with your refresh token
baton-docusign \
  --demo=true \
  --clientId "YOUR_CLIENT_ID" \
  --clientSecret "YOUR_CLIENT_SECRET" \
  --redirect-uri "YOUR_REDIRECT_URI" \
  --refresh-token "YOUR_REFRESH_TOKEN"

baton resources
```

# Data Model

`baton-docusign` will pull down information about the following resources:

- Users
- Groups
- Signing Groups
- Permission Profiles

# Contributing, Support and Issues

We started Baton because we were tired of taking screenshots and manually
building spreadsheets. We welcome contributions, and ideas, no matter how
small&mdash;our goal is to make identity and permissions sprawl less painful for
everyone. If you have questions, problems, or ideas: Please open a GitHub Issue!

See [CONTRIBUTING.md](https://github.com/ConductorOne/baton/blob/main/CONTRIBUTING.md) for more details.

# `baton-docusign` Command Line Usage

```
baton-docusign

Usage:
  baton-docusign [flags]
  baton-docusign [command]

Available Commands:
  capabilities       Get connector capabilities
  completion         Generate the autocompletion script for the specified shell
  config             Get the connector config schema
  help               Help about any command

Flags:
      --client-id string                                 The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string                             The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
      --clientId string                                  required: OAuth 2.0 Client ID from DocuSign ($BATON_CLIENTID)
      --clientSecret string                              required: OAuth 2.0 Client Secret from DocuSign ($BATON_CLIENTSECRET)
      --configure                                        Get the refresh token the first time you run the connector ($BATON_CONFIGURE)
      --demo                                             Set to true for demo environment, false for production ($BATON_DEMO) (default true)
      --external-resource-c1z string                     The path to the c1z file to sync external baton resources with ($BATON_EXTERNAL_RESOURCE_C1Z)
      --external-resource-entitlement-id-filter string   The entitlement that external users, groups must have access to sync external baton resources ($BATON_EXTERNAL_RESOURCE_ENTITLEMENT_ID_FILTER)
  -f, --file string                                      The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                                             help for baton-docusign
      --include-signing-groups                           Set to true to include syncing signing groups (for customers with signing groups feature enabled) ($BATON_INCLUDE_SIGNING_GROUPS)
      --log-format string                                The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string                                 The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
      --otel-collector-endpoint string                   The endpoint of the OpenTelemetry collector to send observability data to ($BATON_OTEL_COLLECTOR_ENDPOINT)
  -p, --provisioning                                     This must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --redirect-uri string                              required: Redirect URI registered in your DocuSign integration ($BATON_REDIRECT_URI)
      --refresh-token string                             OAuth 2.0 Refresh Token for DocuSign (obtain via --configure) ($BATON_REFRESH_TOKEN)
      --skip-full-sync                                   This must be set to skip a full sync ($BATON_SKIP_FULL_SYNC)
      --ticketing                                        This must be set to enable ticketing support ($BATON_TICKETING)
  -v, --version                                          version for baton-docusign

Use "baton-docusign [command] --help" for more information about a command.
```
