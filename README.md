# slackie

Tiny Slack CLI split into two binaries:

- **`slackie`** — the client. Every command that touches Slack or the message
  store is forwarded to the daemon over a unix socket; it never talks to Slack
  itself (except during `setup`, to verify tokens).
- **`slackied`** — the daemon. It owns a local message store, backfills it from
  Slack, and keeps listening for new messages forever.

Built with Rust, Tokio, and [`error-stack`](https://github.com/yaahdev/error-stack).

## Build

```sh
cargo build --release
```

Binaries: `target/release/slackie`, `target/release/slackied`.

## Usage

```sh
# Choose app setup or impersonation setup (writes config, nudges the daemon)
slackie setup

# Start the daemon in the foreground (optional; clients auto-spawn it)
slackied

# Print unread messages from the local store, or block until the next one
slackie read
slackie read --wait

# Send stdin text to a channel or user (daemon performs the API call)
printf 'hello' | slackie send "#team-chat"
printf 'hello' | slackie send "@brad"

# Reply in a thread using target printed by read/send
printf 'thanks' | slackie send "#team-chat:1740000000.123456"

# Upload local files with optional message text
printf 'see attached' | slackie send --attach image.png "@brad"

# Force an immediate backfill pass
slackie sync

# Remove saved credentials
slackie reset
```

`send` accepts channel names (`#channel`), user names (`@user`), Slack
conversation IDs, and user IDs, resolving targets against the local store first
and falling back to the Slack API. Repeat `--attach` for multiple files; use
`--no-mrkdwn` to disable Slack mrkdwn in text messages.

## The daemon

`slackied` listens on `$XDG_CONFIG_HOME/slackie/slackie.sock`
(`~/.config/slackie/slackie.sock` by default) and does all the Slack work:

- **Local store** — a [Turso](https://github.com/tursodatabase/turso) database
  (the ground-up SQLite rewrite, not the libSQL fork) at
  `$XDG_STATE_HOME/slackie/store.db` (`~/.local/state/slackie/store.db`),
  with tables for `users`, `channels`, and `messages`.
- **Backfill** — on startup and every five minutes it loops through every
  conversation the account belongs to and inserts all missing messages,
  including thread replies. Restarts therefore pick up exactly what was
  missed while the daemon was down.
- **Live messages** — the daemon holds a Socket Mode (app token) or RTM
  (impersonation session) connection and inserts new messages as they arrive,
  waking any blocked `slackie read --wait`.
- **Throttling** — all Slack requests go through a shared rate limiter
  (max 4 in flight, one new request every 200 ms) plus 429 backoff, so bulk
  backfill never floods Slack.
- **Safety** — a lock file keeps a single daemon instance; if the configured
  workspace changes, the store is wiped and rebuilt.

Logs go to stderr and to `$XDG_STATE_HOME/slackie/slackied.log`. If a client
finds no daemon, it spawns `slackied` from its own directory or `PATH` and
waits for the socket.

If `read` finds nothing and `--wait` was passed, the request blocks on the
socket until the daemon records a matching message.

### Setup modes

- **App setup** creates a Slack app from a generated manifest; the bot reads
  only channels it is invited to, and the daemon uses Socket Mode for live
  events.
- **Impersonation setup** acts as you, using your own Slack session: read and
  send as you, and listen for new messages over RTM — no Slack app required.
  `read` tracks a `last_seen` cursor in the store (seeded from setup time)
  rather than relying on Slack's unread markers. `setup` tells you which two
  values to copy from a browser where you are logged into Slack (DevTools →
  Application tab). If slackie ever fails with `invalid_auth`, rerun
  `slackie setup` for fresh values.

`send` resolves user mentions written as `<@alice>` or `<@Alice Baker>` to
Slack user IDs. `setup` writes credentials to
`$XDG_CONFIG_HOME/slackie/config.json`, or `~/.config/slackie/config.json`
when `XDG_CONFIG_HOME` is unset. Config file permissions are restricted to the
current user on Unix.

Read preferences (`read_app_mentions_only`, `read_dms`,
`read_private_channels`, `read_message_groups`) filter what `read` prints; the
store still keeps every conversation so filtering never requires another round
trip to Slack.
