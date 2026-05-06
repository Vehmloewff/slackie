# slackie

Tiny Slack Web API CLI using Slack OAuth with PKCE. It installs a bot token for app messages and a small user token for user lookup.

Usage examples:

```sh
SLACKIE_CLIENT_ID=1234567890.1234567890 SLACKIE_APP_TOKEN=xapp-... slackie auth
slackie read
slackie read --wait
printf 'ship it' | slackie send "#backend"
printf 'hello' | slackie send "@alice"
printf 'reply in thread' | slackie send "#backend:1740000000.123456"
printf 'see attached' | slackie send --attach ./report.pdf "#backend"
slackie unauth
```

`auth` requests bot scopes with Slack's `scope` authorize parameter and `users:read` as a user scope. Bot scopes require a public HTTPS web URL redirect URI. Use:

```text
https://example.com/slackie/callback
```

## Slack app scopes

Configure **OAuth & Permissions → Bot Token Scopes** with:

- `app_mentions:read`
- `chat:write`
- `channels:history`, `channels:read`
- `groups:history`, `groups:read`
- `im:history`, `im:read`
- `mpim:history`, `mpim:read`
- `users:read`
- `files:read`, `files:write` (only needed for attachments)

For `read --wait` with Socket Mode, also create an **App-Level Token** with:

- `connections:write`

Then pass it as `SLACKIE_APP_TOKEN=xapp-...` when running `slackie auth`; it is saved in the slackie config for future `read --wait` runs. Subscribe the app to bot events:

- `app_mention`
- `message.im`
- `message.mpim` (optional)

Configure **OAuth & Permissions → User Token Scopes** with:

- `users:read`

Add this redirect URL exactly in **OAuth & Permissions → Redirect URLs**:

```text
https://example.com/slackie/callback
```

After approving, Slack redirects to example.com. That's OK: copy the full URL from the address bar and paste it into `slackie auth`. The OAuth code is protected by PKCE, so the code alone is not sufficient to authenticate.

If your app uses a different web redirect URI, set `SLACKIE_REDIRECT_URI` to that exact value.

`slackie read --wait` requires a Socket Mode app-level token saved by `slackie auth`. You can override the saved token for a single run with `SLACKIE_APP_TOKEN=xapp-... slackie read --wait`.
