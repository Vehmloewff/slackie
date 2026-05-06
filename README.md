# slackie

Tiny Slack CLI that can read and send messages. Supports attachments and notifications.

During setup, you can choose whether the Slack app should receive only messages that mention the bot or all messages from channels the app is installed in and subscribed to. You can also choose whether to allow receiving DMs, private-channel messages, and multi-person DM/message-group messages.

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
