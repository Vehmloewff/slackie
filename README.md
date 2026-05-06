# slackie

Tiny Slack Web API CLI using Slack OAuth with PKCE. It installs a bot token for app messages and a small user token for user lookup.

## Usage

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

## Configuring the Slack app

`slackie` requests bot scopes with Slack's `scope` authorize parameter and `users:read` with `user_scope`. Use the included [`slackbot_manifest.json`](./slackbot_manifest.json) when creating or updating the Slack app; it defines the required redirect URL, OAuth scopes, PKCE, Socket Mode, and event subscriptions.

1. Create a Slack app at <https://api.slack.com/apps> by importing `slackbot_manifest.json`.
2. Create an app-level token with `connections:write` for Socket Mode.
3. Run:

   ```sh
   SLACKIE_CLIENT_ID=<client-id> SLACKIE_APP_TOKEN=xapp-... slackie auth
   ```

4. After approving access, Slack redirects to `https://example.com/slackie/callback?...`. That's OK: copy the full final URL from the browser address bar and paste it back into the terminal.

If you use a different HTTPS redirect URI, update the manifest and set `SLACKIE_REDIRECT_URI` to the same value before running `slackie auth`.

`slackie read --wait` requires a Socket Mode app-level token saved by `slackie auth`. You can override the saved token for a single run with `SLACKIE_APP_TOKEN=xapp-... slackie read --wait`.
