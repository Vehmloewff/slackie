# slackie

Tiny Slack CLI that can read and send messages. Supports attachments and notifications.

## Usage

```sh
slackie setup
slackie read
slackie read --wait
printf 'ship it' | slackie send "#backend"
printf 'hello' | slackie send "@alice"
printf 'reply in thread' | slackie send "#backend:1740000000.123456"
printf 'see attached' | slackie send --attach ./report.pdf "#backend"
slackie reset
```
