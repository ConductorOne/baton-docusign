![Baton Logo](./baton-logo.png)

# `baton-docusign` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-docusign.svg)](https://pkg.go.dev/github.com/conductorone/baton-docusign) ![ci](https://github.com/conductorone/baton-docusign/actions/workflows/ci.yaml/badge.svg) ![verify](https://github.com/conductorone/baton-docusign/actions/workflows/verify.yaml/badge.svg)

`baton-docusign` is a connector for DocuSign built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the DocuSign eSignature REST API v2.1 to sync users, groups, signing groups, and permission profiles. It can optionally also sync DocuSign CLM (Contract Lifecycle Management) folders, folder security, groups, and permission sets — see [CLM Support](#clm-support-optional) below.

Check out [Baton](https://github.com/conductorone/baton) to learn more about the project in general.

## Connector Capabilities

### Resources Synced

- Users
- Groups
- Signing Groups
- Permission Profiles
- CLM Members, Roles, Groups, Folders, Folder Security, and Permission Sets (optional — see [CLM Support](#clm-support-optional))

### Provisioning Support

- Users (create accounts)
- Group membership (grant/revoke)
- Signing group membership (grant/revoke)
- Permission profiles (grant only - users must always have a profile assigned)
- CLM group membership (grant/revoke, optional)
- CLM folder security (grant/revoke, optional)
- CLM permission sets are synced for visibility only — the CLM API has no assignment endpoint, so they cannot be granted or revoked

## Connector Credentials

The credentials required depend on the environment:

**Production** (using ConductorOne's managed OAuth app, cloud-hosted only):
- No custom DocuSign app credentials needed — authentication is handled via ConductorOne's OAuth flow.

**Demo environment** (DocuSign's `account-d.docusign.com`) **or self-hosted**:
1. **Client ID** (Integration Key)
2. **Client Secret**
3. **Redirect URI**
4. **Refresh Token**
5. **Account ID** (optional — API Account ID UUID, only needed if you have multiple DocuSign accounts)

> **Note:** When running the connector with `--docusign-client-id`, demo mode is automatically enabled. The `--demo` flag is optional in that case.

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

## CLM Support (optional)

DocuSign CLM (Contract Lifecycle Management) is a separate DocuSign product from
eSignature, with its own API and a separate production subscription. Set the
`--include-clm` flag (or `BATON_INCLUDE_CLM=true`) to sync CLM members, roles, groups,
folders, folder security, and permission sets alongside the standard eSignature
resources.

Requirements:

- Your DocuSign account must have a CLM production subscription.
- **Demo environment or self-hosted with your own DocuSign app**: no extra setup — the
  connector requests the additional CLM OAuth scopes (`spring_read`/`spring_write`)
  automatically when `--include-clm` is set.
- **Cloud-hosted production (ConductorOne's managed OAuth app)**: the managed app must
  also be granted the CLM API scope on ConductorOne's platform side before this flag
  will have any effect. Contact ConductorOne if enabling `--include-clm` doesn't sync
  any CLM data in this mode.

CLM permission sets sync for visibility only — DocuSign's CLM API has no endpoint to
assign or unassign a permission set, so they cannot be granted or revoked through this
connector.

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
  -e BATON_DOCUSIGN_CLIENT_ID=YOUR_CLIENT_ID \
  -e BATON_DOCUSIGN_CLIENT_SECRET=YOUR_CLIENT_SECRET \
  -e BATON_REDIRECT_URI=YOUR_REDIRECT_URI \
  ghcr.io/conductorone/baton-docusign:latest --configure

# Then, run the connector with your refresh token
docker run --rm -v $(pwd):/out \
  -e BATON_DEMO=true \
  -e BATON_DOCUSIGN_CLIENT_ID=YOUR_CLIENT_ID \
  -e BATON_DOCUSIGN_CLIENT_SECRET=YOUR_CLIENT_SECRET \
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
- CLM Members, Roles, Groups, Folders, Folder Security, and Permission Sets (optional, requires `--include-clm` and a DocuSign CLM subscription)

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
  health-check       Check the health of a running connector
  help               Help about any command

Flags:
      --account-id string                                API account ID (UUID format) of the DocuSign account to be used for synchronization. Leave blank to use your default account. Warning: changing this ID between different synchronizations may result in data loss. If you want to synchronize different accounts, create different connectors. ($BATON_ACCOUNT_ID)
      --client-id string                                 The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string                             The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
      --configure                                        Get the refresh token the first time you run the connector. ($BATON_CONFIGURE)
      --demo                                             Set to true for demo environment, false for production ($BATON_DEMO)
      --docusign-client-id string                        required: OAuth 2.0 Client ID from your DocuSign developer app ($BATON_DOCUSIGN_CLIENT_ID)
      --docusign-client-secret string                    required: OAuth 2.0 Client Secret from your DocuSign developer app ($BATON_DOCUSIGN_CLIENT_SECRET)
      --external-resource-c1z string                     The path to the c1z file to sync external baton resources with ($BATON_EXTERNAL_RESOURCE_C1Z)
      --external-resource-entitlement-id-filter string   The entitlement that external users, groups must have access to sync external baton resources ($BATON_EXTERNAL_RESOURCE_ENTITLEMENT_ID_FILTER)
  -f, --file string                                      The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                                             help for baton-docusign
      --include-clm                                      Set to true to include syncing DocuSign CLM folders, folder security, groups, and permission sets. Requires a DocuSign CLM production subscription. When using the default OAuth Authentication method, this also requires ConductorOne's managed OAuth app to be granted the CLM API scope — contact ConductorOne if enabling this has no effect. ($BATON_INCLUDE_CLM)
      --include-signing-groups                           Set to true to include syncing signing groups (for customers with signing groups feature enabled) ($BATON_INCLUDE_SIGNING_GROUPS)
      --log-format string                                The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string                                 The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
      --oauth2-token string                              OAuth 2.0 Authentication for DocuSign ($BATON_OAUTH2_TOKEN)
      --otel-collector-endpoint string                   The endpoint of the OpenTelemetry collector to send observability data to (used for both tracing and logging if specific endpoints are not provided) ($BATON_OTEL_COLLECTOR_ENDPOINT)
  -p, --provisioning                                     This must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --redirect-uri string                              Redirect URI registered in your DocuSign integration ($BATON_REDIRECT_URI)
      --refresh-token string                             OAuth 2.0 Refresh Token for DocuSign ($BATON_REFRESH_TOKEN)
      --skip-full-sync                                   This must be set to skip a full sync ($BATON_SKIP_FULL_SYNC)
      --ticketing                                        This must be set to enable ticketing support ($BATON_TICKETING)
  -v, --version                                          version for baton-docusign

Use "baton-docusign [command] --help" for more information about a command.
```

> Additional flags inherited from the Baton SDK (health checks, worker/concurrency
> tuning, storage engine selection, etc.) are omitted above for brevity — run
> `baton-docusign --help` for the full list.
