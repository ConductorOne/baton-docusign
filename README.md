![Baton Logo](./baton-logo.png)

# `baton-docusign` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-docusign.svg)](https://pkg.go.dev/github.com/conductorone/baton-docusign) ![ci](https://github.com/conductorone/baton-docusign/actions/workflows/ci.yaml/badge.svg) ![verify](https://github.com/conductorone/baton-docusign/actions/workflows/verify.yaml/badge.svg)

`baton-docusign` is a connector for DocuSign built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the DocuSign eSignature REST API v2.1 to sync users, groups, signing groups, and permission profiles. It also syncs DocuSign CLM (Contract Lifecycle Management) folders, folder security, groups, and permission sets for accounts with a CLM subscription — see [CLM Support](#clm-support) below.

Check out [Baton](https://github.com/conductorone/baton) to learn more about the project in general.

## Connector Capabilities

### Resources Synced

- Users
- Groups
- Signing Groups
- Permission Profiles
- CLM Members, Roles, Groups, Folders, Folder Security, Permission Sets, and Workflow Queues (requires a DocuSign CLM subscription — see [CLM Support](#clm-support))

### Provisioning Support

- Users (create accounts)
- Group membership (grant/revoke)
- Signing group membership (grant/revoke)
- Permission profiles (grant only - users must always have a profile assigned)
- CLM group membership (grant/revoke, requires a CLM subscription)
- CLM folder security (grant/revoke, requires a CLM subscription)
- CLM permission sets are synced for visibility only — the CLM API has no assignment endpoint, so they cannot be granted or revoked
- CLM workflow queue membership is synced for visibility only — the CLM API supports work-item assign/unassign, not queue-membership grant/revoke, so it cannot be granted or revoked here

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

## CLM Support

DocuSign CLM (Contract Lifecycle Management) is a separate DocuSign product from
eSignature, with its own API and a separate production subscription. CLM members, roles,
groups, folders, folder security, permission sets, and workflow queues sync alongside the
standard eSignature resources, with no config flag to enable — accounts that don't have
CLM simply sync no CLM resources.

Requirements:

- Your DocuSign account must have a CLM production subscription.
- **Demo environment or self-hosted with your own DocuSign app**: no extra setup — the
  connector requests the additional CLM OAuth scopes (`spring_read`/`spring_write`)
  automatically.
- **Cloud-hosted production (ConductorOne's managed OAuth app)**: the managed app must
  also be granted the CLM API scopes on ConductorOne's platform side before any CLM data
  will sync. Contact ConductorOne if no CLM data appears in this mode.

The 6 CLM resource types are always registered and visible to C1 — this avoids a C1 sync
engine treating CLM resources as deleted if they stop appearing (see
[CHANGE_TYPES.md](CHANGE_TYPES.md) if you're touching this). Without the CLM OAuth scopes
(or without a CLM subscription on the account), each CLM resource type's sync is skipped
gracefully rather than erroring the whole sync.

CLM permission sets sync for visibility only — DocuSign's CLM API has no endpoint to
assign or unassign a permission set, so they cannot be granted or revoked through this
connector.

CLM workflow queues (`clm_workflow_queue`) map to what the CLM admin console reportedly
calls "Task Groups" — that equivalence is an unconfirmed assumption, not a documented
fact, since no live CLM admin console was available to check it against. The CLM API has
no list-all endpoint for workflow queues and no reverse lookup from a queue to its
members, so this connector discovers them by scanning every `clm_member`'s own workflow
queues and deduping — one API call per member, on top of the member sync itself. This
adds meaningful request volume on large accounts; consider this before enabling it on an
account already seeing rate-limit errors. Workflow
queue membership syncs for visibility only — the API supports work-item assign/unassign,
not queue-membership grant/revoke, so it cannot be granted or revoked through this
connector.

The CLM Object API's base URL is resolved via a separate account discovery call
(`GET /api/v2/{accountId}/account` on `auth.springcm.com`/`authuat.springcm.com`,
authenticated with the same access token), confirmed via DocuSign's CLM API 101
documentation. That endpoint's exact response schema was not available at
implementation time, so the connector checks a short list of likely field names
(`ApiBaseUrl`, `api_base_url`, `ObjectApiUrl`, and similar) and fails with the actual
field names it received if none match — that error message is the first thing to check
if the connector can't resolve the CLM base URL against a real account.

Folder discovery (`SearchFolders`) sends an empty search body, on the assumption that
no criteria means "match all folders." This has not been confirmed against a live CLM
account — if it instead means "no criteria, no results," no folders (and therefore no
folder-security grants) would sync while the connector reports success. If a real CLM
account syncs zero `clm_folder` resources, this is the first thing to check.

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
- CLM Members, Roles, Groups, Folders, Folder Security, and Permission Sets (requires a DocuSign CLM subscription)

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
      --auth-method string                               ($BATON_AUTH_METHOD)
      --client-id string                                 The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string                             The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
      --configure                                        Get the refresh token the first time you run the connector. ($BATON_CONFIGURE)
      --demo                                             Set to true for demo environment, false for production ($BATON_DEMO)
      --docusign-client-id string                        required: OAuth 2.0 Client ID from your DocuSign developer app ($BATON_DOCUSIGN_CLIENT_ID)
      --docusign-client-secret string                    required: OAuth 2.0 Client Secret from your DocuSign developer app ($BATON_DOCUSIGN_CLIENT_SECRET)
      --external-resource-c1z string                     The path to the c1z file to sync external baton resources with ($BATON_EXTERNAL_RESOURCE_C1Z)
      --external-resource-entitlement-id-filter string   The entitlement that external users, groups must have access to sync external baton resources ($BATON_EXTERNAL_RESOURCE_ENTITLEMENT_ID_FILTER)
  -f, --file string                                      The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
      --health-check                                     Enable the HTTP health check endpoint ($BATON_HEALTH_CHECK)
      --health-check-port int                            Port for the HTTP health check endpoint ($BATON_HEALTH_CHECK_PORT) (default 8081)
  -h, --help                                             help for baton-docusign
      --http-timeout-seconds int                         HTTP client timeout in seconds (max 1800) ($BATON_HTTP_TIMEOUT_SECONDS) (default 300)
      --include-signing-groups                           Set to true to sync signing groups (for customers with the signing groups feature enabled on their account). ($BATON_INCLUDE_SIGNING_GROUPS)
      --keep-previous-sync-c1z                           Keep the previously synced c1z on disk to enable ETag replay across service-mode syncs (requires a connector that supports ETag replay; costs one c1z of local disk) ($BATON_KEEP_PREVIOUS_SYNC_C1Z)
      --log-format string                                The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string                                 The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
      --log-level-debug-expires-at string                The timestamp indicating when debug-level logging should expire ($BATON_LOG_LEVEL_DEBUG_EXPIRES_AT)
      --log-path strings                                 The file path to write logs to ($BATON_LOG_PATH)
      --oauth2-token string                              OAuth 2.0 Authentication for DocuSign ($BATON_OAUTH2_TOKEN)
      --otel-collector-endpoint string                   The endpoint of the OpenTelemetry collector to send observability data to (used for both tracing and logging if specific endpoints are not provided) ($BATON_OTEL_COLLECTOR_ENDPOINT)
      --parallel-sync                                    Deprecated: use --workers instead. ($BATON_PARALLEL_SYNC)
  -p, --provisioning                                     This must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --redirect-uri string                              Redirect URI registered in your DocuSign integration ($BATON_REDIRECT_URI)
      --refresh-token string                             OAuth 2.0 Refresh Token for DocuSign ($BATON_REFRESH_TOKEN)
      --skip-entitlements-and-grants                     This must be set to skip syncing of entitlements and grants ($BATON_SKIP_ENTITLEMENTS_AND_GRANTS)
      --skip-full-sync                                   This must be set to skip a full sync ($BATON_SKIP_FULL_SYNC)
      --storage-engine string                            The storage engine to use when opening the sync c1z file: sqlite or pebble. Leave unset to use the baton-sdk default. ($BATON_STORAGE_ENGINE)
      --sync-resource-types strings                      The resource type IDs to sync ($BATON_SYNC_RESOURCE_TYPES)
      --sync-resources strings                           The resource IDs to sync ($BATON_SYNC_RESOURCES)
      --task-concurrency int                             The number of Baton tasks to run concurrently in service mode. Tasks may include sync, grant, revoke, and more. Minimum value is 1, maximum value is 100. ($BATON_TASK_CONCURRENCY) (default 3)
      --ticketing                                        This must be set to enable ticketing support ($BATON_TICKETING)
  -v, --version                                          version for baton-docusign
      --workers int                                      The number of sync workers to use. -1 for auto-detect, 0 for sequential, >0 for parallel ($BATON_WORKERS)

Use "baton-docusign [command] --help" for more information about a command.
```

> Generated from `baton-docusign --help` against baton-sdk v0.17.0. Hidden flags
> (`--base-url`, `--clm-base-url`) are testing-only and intentionally absent.
