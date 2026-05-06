package main

import (
	"encoding/json"
	"fmt"
)

var manifestBotScopes = []string{
	"files:write",
	"app_mentions:read",
	"channels:history",
	"channels:join",
	"channels:read",
	"chat:write",
	"dnd:read",
	"emoji:read",
	"files:read",
	"groups:history",
	"im:history",
	"im:read",
	"links:read",
	"metadata.message:read",
	"mpim:history",
	"mpim:read",
	"pins:read",
	"reactions:read",
	"reactions:write",
	"reminders:read",
	"reminders:write",
	"search:read.files",
	"search:read.im",
	"search:read.mpim",
	"search:read.private",
	"search:read.public",
	"search:read.users",
	"team:read",
	"users.profile:read",
	"users:read",
	"users:read.email",
	"users:write",
	"groups:read",
	"im:write",
}

func slackAppManifest(botName string) (string, error) {
	manifest := map[string]any{
		"display_information": map[string]any{
			"name": botName,
		},
		"features": map[string]any{
			"bot_user": map[string]any{
				"display_name":  botName,
				"always_online": false,
			},
		},
		"oauth_config": map[string]any{
			"scopes": map[string]any{
				"bot": manifestBotScopes,
			},
		},
		"settings": map[string]any{
			"event_subscriptions": map[string]any{
				"bot_events": []string{"app_mention", "reaction_added"},
			},
			"interactivity": map[string]any{
				"is_enabled": true,
			},
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    true,
			"token_rotation_enabled": false,
			"is_mcp_enabled":         false,
		},
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode Slack app manifest: %w", err)
	}
	return string(data), nil
}
