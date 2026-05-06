package main

import (
	"encoding/json"
	"fmt"
)

type readSettings struct {
	ReadAppMentionsOnly bool
	ReadDMs             bool
	ReadPrivateChannels bool
	ReadMessageGroups   bool
}

var manifestBaseBotScopes = []string{
	"channels:history",
	"channels:join",
	"channels:read",
	"chat:write",
	"files:read",
	"files:write",
	"im:write",
	"users:read",
}

func manifestBotScopes(settings readSettings) []string {
	scopes := append([]string(nil), manifestBaseBotScopes...)
	if settings.ReadAppMentionsOnly {
		scopes = append(scopes, "app_mentions:read")
	}
	if settings.ReadDMs {
		scopes = append(scopes, "im:history", "im:read")
	}
	if settings.ReadPrivateChannels {
		scopes = append(scopes, "groups:history", "groups:read")
	}
	if settings.ReadMessageGroups {
		scopes = append(scopes, "mpim:history", "mpim:read")
	}
	return scopes
}

func manifestBotEvents(settings readSettings) []string {
	events := []string{}
	if settings.ReadAppMentionsOnly {
		events = append(events, "app_mention")
	} else {
		events = append(events, "message.channels")
		if settings.ReadPrivateChannels {
			events = append(events, "message.groups")
		}
	}
	if settings.ReadDMs {
		events = append(events, "message.im")
	}
	if settings.ReadMessageGroups {
		events = append(events, "message.mpim")
	}
	return events
}

func slackAppManifest(botName string, settings readSettings) (string, error) {
	features := map[string]any{
		"bot_user": map[string]any{
			"display_name":  botName,
			"always_online": false,
		},
	}
	if settings.ReadDMs {
		features["app_home"] = map[string]any{
			"messages_tab_enabled":           true,
			"messages_tab_read_only_enabled": false,
		}
	}

	manifest := map[string]any{
		"display_information": map[string]any{
			"name": botName,
		},
		"features": features,
		"oauth_config": map[string]any{
			"scopes": map[string]any{
				"bot": manifestBotScopes(settings),
			},
		},
		"settings": map[string]any{
			"event_subscriptions": map[string]any{
				"bot_events": manifestBotEvents(settings),
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
