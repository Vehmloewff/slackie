package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type socketModeOpenResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	URL   string `json:"url"`
}

type socketModeEnvelope struct {
	EnvelopeID string          `json:"envelope_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	Reason     string          `json:"reason"`
	DebugInfo  json.RawMessage `json:"debug_info"`
}

type socketModeEventsPayload struct {
	Event socketModeEvent `json:"event"`
}

type socketModeEvent struct {
	Type        string            `json:"type"`
	Subtype     string            `json:"subtype"`
	User        string            `json:"user"`
	Text        string            `json:"text"`
	TS          string            `json:"ts"`
	ThreadTS    string            `json:"thread_ts"`
	Channel     string            `json:"channel"`
	ChannelType string            `json:"channel_type"`
	BotID       string            `json:"bot_id"`
	Username    string            `json:"username"`
	Files       []slackFile       `json:"files"`
	Attachments []slackAttachment `json:"attachments"`
}

func socketModeAppToken(cfg Config) string {
	return strings.TrimSpace(cfg.AppToken)
}

func openSocketModeURL(ctx context.Context, appToken string) (string, error) {
	client := &SlackClient{HTTPClient: &http.Client{Timeout: 30 * time.Second}, Token: appToken}
	var out socketModeOpenResponse
	if err := client.apiForm(ctx, "apps.connections.open", nil, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.URL) == "" {
		return "", fmt.Errorf("apps.connections.open returned an empty websocket URL")
	}
	return out.URL, nil
}

func waitForSocketModeMessage(ctx context.Context, client *SlackClient, appToken string, userNames map[string]string, myUserID string, cfg *Config) error {
	wsURL, err := openSocketModeURL(ctx, appToken)
	if err != nil {
		return fmt.Errorf("open socket mode connection: %w", err)
	}

	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect socket mode websocket: %w", err)
	}
	defer conn.Close()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read socket mode websocket: %w", err)
		}

		var env socketModeEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("decode socket mode envelope: %w", err)
		}

		if env.EnvelopeID != "" {
			if err := conn.WriteJSON(map[string]string{"envelope_id": env.EnvelopeID}); err != nil {
				return fmt.Errorf("ack socket mode envelope: %w", err)
			}
		}

		switch env.Type {
		case "hello":
			continue
		case "disconnect":
			return fmt.Errorf("socket mode disconnected: %s", emptyFallback(env.Reason, "unknown_reason"))
		case "events_api":
			item, ok, err := unreadFromSocketEvent(ctx, client, env.Payload, userNames, myUserID)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := printUnreadConversation(ctx, client, item, userNames, myUserID); err != nil {
				return fmt.Errorf("print %s: %w", conversationTitle(item.Conv, userNames), err)
			}
			rememberLatest(cfg, item.Conv.ID, item.Messages)
			return nil
		}
	}
}

func unreadFromSocketEvent(ctx context.Context, client *SlackClient, payload json.RawMessage, userNames map[string]string, myUserID string) (unreadConversation, bool, error) {
	var p socketModeEventsPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return unreadConversation{}, false, fmt.Errorf("decode socket mode event payload: %w", err)
	}
	event := p.Event
	if event.Channel == "" || event.TS == "" {
		return unreadConversation{}, false, nil
	}
	if event.User == myUserID || event.BotID != "" {
		return unreadConversation{}, false, nil
	}

	isDM := event.Type == "message" && event.ChannelType == "im"
	isMPIM := event.Type == "message" && event.ChannelType == "mpim"
	isMention := event.Type == "app_mention" || (myUserID != "" && strings.Contains(event.Text, "<@"+myUserID+">") && event.ChannelType != "im" && event.ChannelType != "mpim")
	if !isDM && !isMPIM && !isMention {
		return unreadConversation{}, false, nil
	}

	conv := conversation{ID: event.Channel, IsIM: isDM, IsMPIM: isMPIM}
	if isDM {
		conv.User = event.User
	} else {
		if info, err := client.getConversationInfo(ctx, event.Channel); err == nil {
			conv = info
			conv.ID = event.Channel
		} else if isMPIM {
			conv.IsMPIM = true
		}
	}

	msg := message{
		Type:        "message",
		User:        event.User,
		Text:        event.Text,
		TS:          event.TS,
		ThreadTS:    event.ThreadTS,
		Subtype:     event.Subtype,
		BotID:       event.BotID,
		Username:    event.Username,
		Files:       event.Files,
		Attachments: event.Attachments,
	}
	return unreadConversation{Conv: conv, Messages: []message{msg}}, true, nil
}
