# slackie

Tiny Slack CLI for auth, reading unreads, and sending messages.

## Install

Download the binary for your platform from the [GitHub Releases page](https://github.com/Vehmloewff/slackie/releases), make it executable, and put it on your `PATH`.

## Usage

```sh
slackie auth
slackie read
slackie read --wait
printf 'ship it' | slackie send "#backend"
printf 'hello' | slackie send "@alice"
printf 'reply in thread' | slackie send "#backend:1740000000.123456"
printf 'reply in thread' | slackie send "C12345678:1740000000.123456"
printf 'see attached' | slackie send --attach ./report.pdf "#backend"
slackie unauth
```

### Commands

- `slackie auth` — sign in
- `slackie read` — print unread messages, including per-message thread reply targets, and mark them read
- `slackie read --wait` — wait for the next message if nothing is unread
- `slackie send [--attach PATH ...] <target>` — send stdin to a channel, DM, or thread
- `slackie unauth` — remove saved local auth state
