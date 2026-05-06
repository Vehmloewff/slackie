# Configuring the Slack App

`slackie` uses Slack OAuth with PKCE. It requests **Bot Token Scopes** via Slack's `scope` authorize parameter for app messages, plus `users:read` via `user_scope` for user-name lookup.

1. Create a Slack App at <https://api.slack.com/apps>.
2. In **OAuth & Permissions**, add this redirect URL exactly:

   ```text
   https://example.com/slackie/callback
   ```

3. In **OAuth & Permissions**, add the bot token scopes and user token scopes from `README.md`.
4. For reliable `read --wait`, enable **Socket Mode**, create an app-level token with `connections:write`, and subscribe to the bot events `app_mention`, `message.im`, and optionally `message.mpim`.
5. Run:

   ```sh
   SLACKIE_CLIENT_ID=<client-id> slackie auth
   ```

6. After approving access, Slack redirects to `https://example.com/slackie/callback?...`. That's OK. Copy the full final URL from the address bar and paste it back into the terminal.
7. Run `SLACKIE_APP_TOKEN=xapp-... slackie auth` once to save the app-level token, then use Socket Mode waits with `slackie read --wait`.

Notes:

- If you use a different web redirect URI, add that exact URI in Slack and set `SLACKIE_REDIRECT_URI` too.
- This flow does not use a local callback server.
