# slackie

Tiny single-file Go CLI for a few practical Slack tasks.

## Slack app setup

Create a Slack app and configure these settings:

- OAuth 2.0 redirect URL: add the exact redirect URI you will use in the CLI
- User token scopes:
  - `chat:write`
  - `channels:history`
  - `groups:history`
  - `im:history`
  - `mpim:history`
  - `channels:read`
  - `groups:read`
  - `im:read`
  - `mpim:read`
  - `users:read`

This CLI uses **user OAuth with PKCE**.
It does **not** use a bot token, local callback server, browser auto-open, or websockets.

## Redirect URI behavior

The redirect URI configured in Slack must **exactly match** the value compiled into the CLI as `slackRedirectURI`.

The CLI does not serve the redirect URI. Instead:

1. Run `slackie auth`
2. Copy the printed Slack authorize URL into your browser
3. Approve access
4. After Slack redirects, copy the **full final redirected URL** from the browser address bar
5. Paste that URL back into the CLI

The Slack auth code is short-lived, so paste the redirected URL back into the CLI quickly.

## OAuth constants

`slackie auth` uses constants compiled into `main.go`:

- `slackClientID`
- `slackRedirectURI`

These are not treated as secrets in this project.

## Example commands

```sh
slackie auth
slackie unreads
slackie send "#backend" "ship it"
slackie send "@alice" "hello"
slackie send "#backend:1740000000.123456" "reply in thread"
```

## Local config

Auth state is stored as a small JSON file in your OS user config directory, typically something like:

- macOS: `~/Library/Application Support/slackie/config.json`
- Linux: `~/.config/slackie/config.json`
- Windows: `%AppData%\\slackie\\config.json`
