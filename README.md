# slackie

Tiny Slack CLI for reading and sending messages. Built with Rust, Tokio, and [`err`](https://github.com/Vehmloewff/err) error handling.

## Build

```sh
cargo build --release
```

Binary: `target/release/slackie`.

## Usage

```sh
# Choose app setup or impersonation setup
slackie setup

# Print unread messages, or wait for the next matching Socket Mode event
slackie read
slackie read --wait

# Send stdin text to a channel or user
printf 'hello' | slackie send "#team-chat"
printf 'hello' | slackie send "@brad"

# Reply in a thread using target printed by read/send
printf 'thanks' | slackie send "#team-chat:1740000000.123456"

# Upload local files with optional message text
printf 'see attached' | slackie send --attach image.png "@brad"

# Remove saved credentials
slackie reset
```

`send` accepts channel names (`#channel`), user names (`@user`), Slack conversation IDs, and user IDs. Repeat `--attach` for multiple files; use `--no-mrkdwn` to disable Slack mrkdwn in text messages.

### Setup modes

- **App setup** creates a Slack app from a generated manifest; the bot reads only channels it is invited to.
- **Impersonation setup** acts as you, using your own Slack session: read and send as you, and listen for new messages — no Slack app required. `read` tracks a global `last_seen` timestamp in Slackie's config, rather than relying on Slack's unread markers. `setup` tells you which two values to copy from a browser where you are logged into Slack (DevTools → Application tab). If slackie ever fails with `invalid_auth`, rerun `slackie setup` for fresh values.

`send` resolves user mentions written as `<@alice>` or `<@Alice Baker>` to Slack user IDs. `setup` writes credentials to `$XDG_CONFIG_HOME/slackie/config.json`, or `~/.config/slackie/config.json` when `XDG_CONFIG_HOME` is unset. Config file permissions are restricted to the current user on Unix.
