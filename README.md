![Baton Logo](./baton-logo.png)

# `baton-docusign` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-docusign.svg)](https://pkg.go.dev/github.com/conductorone/baton-docusign) ![main ci](https://github.com/conductorone/baton-docusign/actions/workflows/main.yaml/badge.svg)

`baton-docusign` is a connector for built using the [Baton SDK](https://github.com/conductorone/baton-sdk).

Check out [Baton](https://github.com/conductorone/baton) to learn more the project in general.

## Connector Capabilities

1. **Resources synced**:

   - Users
   - Groups
   - Permissions

2. **Account provisioning**

   - Users

## Connector Credentials

1. **ACCOUNT ID**
2. **CLIENT ID**
3. **CLIENT SECRET**
4. **REDIRECT URI**

### Obtaining Credentials

1. Log in to [DocuSign Developer Account.](https://account-d.docusign.com/logout)
2. Go to Admin → Apps and Keys.
3. Copy The User ID.
4. Click on Add App and Integration Key.
5. Click in "Add App and integration Key"
6. Configure the app:
   - Enable User Application **User Application**
   - Click **Add Secret Key** and copy it.
   - Under **Additional Settings**, add your **Redirect URI** (e.g.,"http://example.com/callback")
   - Under **CORS Settings** enable GET, POST, PUT, DELETE, and HEAD.
7. Save the application.

# Getting Started

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-docusign
baton-docusign
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_DOMAIN_URL=domain_url -e BATON_API_KEY=apiKey -e BATON_USERNAME=username ghcr.io/conductorone/baton-docusign:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-docusign/cmd/baton-docusign@main

baton-docusign

baton resources
```

# Data Model

`baton-docusign` will pull down information about the following resources:

- Users
- Groups
- Permissions

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
  help               Help about any command

Flags:
      --account-id string            The API Account ID from DocuSign
      --client-id string             The Integration Key (Client ID) ($BATON_CLIENT_ID)
      --client-secret string         The Client Secret ($BATON_CLIENT_SECRET)
      --redirect-uri string          The Redirect URI used in OAuth2
      --client-id string             The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string         The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
  -f, --file string                  The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                         help for baton-docusign
      --log-format string            The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string             The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -p, --provisioning                 If this connector supports provisioning, this must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --ticketing                    This must be set to enable ticketing support ($BATON_TICKETING)
  -v, --version                      version for baton-docusign

Use "baton-docusign [command] --help" for more information about a command.
```
