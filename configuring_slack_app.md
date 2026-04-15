# Configuring your Slack app

`slackie` uses **user OAuth with PKCE**.
It does **not** use a bot token or a local callback server.

## 1. Create a Slack app

Create an app in your Slack workspace and open **OAuth & Permissions**.

## 2. Add the redirect URL

Add this redirect URL exactly:

```text
slackie://callback
```

The redirect URI must match exactly.

## 3. Add required user token scopes

Add these **User Token Scopes**:

### Messaging

- `chat:write`

### Reading conversations and messages

- `channels:history`
- `groups:history`
- `im:history`
- `mpim:history`
- `channels:read`
- `groups:read`
- `im:read`
- `mpim:read`

### Marking conversations read

- `channels:write`
- `groups:write`
- `im:write`
- `mpim:write`

### User lookup

- `users:read`

### File access

- `files:read`
- `files:write`

## 4. Install or reinstall the app

Install the app to your workspace.
If you change scopes later, reinstall the app and run `slackie auth` again.

## PKCE flow

`slackie auth` uses the OAuth authorization code flow with PKCE:

1. Run:
   ```sh
   slackie auth
   ```
2. The CLI prints a Slack authorization URL.
3. Open that URL in your browser and approve access.
4. Slack redirects to `slackie://callback?...`.
5. Copy the **full final redirected URL** from your browser.
6. Paste that full URL back into the CLI.

Notes:

- The CLI does not open a browser for you.
- The CLI does not listen on a localhost callback port.
- The authorization code is short-lived, so paste the redirected URL back promptly.
- This flow is for a **user token**, not a bot token.
