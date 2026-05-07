# slackie

Tiny Slack CLI that can read and send messages. Supports attachments and notifications.

During setup, you can choose whether the Slack app should receive only messages that mention the bot or all messages from channels the app is installed in and subscribed to. You can also choose whether to allow receiving DMs, private-channel messages, and multi-person DM/message-group messages.

## Usage

```sh
# Authorize slackie with Slack before sending or receiving messages. I'll guide you through configuring and installing the slack app.
slackie setup

# Print unread messages.
slackie read

# Wait for the next unread message if there is nothing to print yet.
slackie read --wait

# Send stdin to a channel; channel names are prefixed with #.
printf 'approved, but not more than $50' | slackie send "#team-chat"

# Send stdin to a user; user names are prefixed with @.
printf 'no you must pay the full $50, or im going to burn down your gazebo also' | slackie send "@brad"

# Mention a user in a channel by including their Slack mention in the message body.
printf 'payment received from <@brad>' | slackie send "#mediation"

# Reply in a thread using the threading target printed by read or send.
printf "thx brad" | slackie send "#mediation:1740000000.123456"

# Send stdin with an attachment using --attach.
printf 'also, what in the world did you do to my toilet?' | slackie send --attach IMG_87812.png "@brad"

# Remove local Slack credentials so setup can be performed again.
slackie reset
```
