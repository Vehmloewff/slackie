# slackie

Tiny Go CLI for a few practical Slack tasks.

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
  - `channels:write`
  - `groups:write`
  - `im:write`
  - `mpim:write`
  - `users:read`

This CLI uses **user OAuth with PKCE**.
It does **not** use a bot token, local callback server, browser auto-open, Socket Mode, or websockets.

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

`slackie auth` uses constants compiled into the Go code:

- `slackClientID`
- `slackRedirectURI`

These are not treated as secrets in this project.

## Commands

```sh
slackie auth
slackie unauth
slackie read
slackie read --wait
slackie send "#backend" "ship it"
slackie send "@alice" "hello"
slackie send "#backend:1740000000.123456" "reply in thread"
```

## `unauth`

### `slackie unauth`

- removes the saved local auth config file
- does not revoke the Slack token remotely
- use `slackie auth` again to sign in again later

## `read`

### `slackie read`

- lists unread conversations
- prints the unread messages it fetched
- marks them read
- exits

### `slackie read --wait`

- first runs the normal unread scan
- if unread messages already exist, it prints them, marks them read, and exits immediately
- if there are no unread messages, it waits for the first newly arrived message in any accessible public channel, private channel, DM, or MPIM
- when that first new message arrives, it prints it in the same style, marks that conversation read at the latest printed message timestamp, and exits immediately

The waiting mode uses simple polling with `conversations.history` every few seconds.

## Important scope note for marking messages read

This CLI calls `conversations.mark` after printing messages.
If you want `slackie read` and `slackie read --wait` to successfully mark conversations as read, make sure your Slack app includes these user scopes before installing and authenticating:

- `channels:write`
- `groups:write`
- `im:write`
- `mpim:write`

If you add scopes later, reinstall the app in Slack and run `slackie auth` again so the saved token includes them.

## Local config

Auth state is stored as a small JSON file in your OS user config directory, typically something like:

- macOS: `~/Library/Application Support/slackie/config.json`
- Linux: `~/.config/slackie/config.json`
- Windows: `%AppData%\\slackie\\config.json`
