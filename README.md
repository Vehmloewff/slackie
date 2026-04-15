# slackie

Tiny Slack CLI for auth, reading unreads, and sending messages.

## Install

Download the binary for your platform from the [GitHub Releases page](https://github.com/Vehmloewff/slackie/releases), make it executable, and put it on your `PATH`.

## Getting started

Before authenticating, configure your Slack app as described in [configuring_slack_app.md](./configuring_slack_app.md).

```sh
slackie auth
slackie read
slackie read --wait
printf 'ship it\n' | slackie send "#backend"
printf 'hello\n' | slackie send "@alice"
printf 'reply in thread\n' | slackie send "#backend:1740000000.123456"
printf 'see attached\n' | slackie send --attach ./report.pdf "#backend"
slackie unauth
```

## Commands

- `slackie auth` — sign in
- `slackie read` — print unread messages and mark them read
- `slackie read --wait` — wait for the next message if nothing is unread
- `slackie send [--attach PATH ...] <target>` — send stdin to a channel, DM, or thread
- `slackie unauth` — remove saved local auth state
