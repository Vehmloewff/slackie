package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFindUnreadConversationsFirstRunScansRecentHistory(t *testing.T) {
	client := &SlackClient{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/conversations.history" {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		if got := req.URL.Query().Get("channel"); got != "C123" {
			t.Fatalf("channel = %q, want C123", got)
		}
		if got := req.URL.Query().Get("limit"); got != "50" {
			t.Fatalf("limit = %q, want 50", got)
		}
		if got := req.URL.Query().Get("oldest"); got != "" {
			t.Fatalf("oldest = %q, want empty on first run", got)
		}
		return jsonResponse(`{"ok":true,"messages":[{"type":"message","ts":"1778108731.000100","user":"U1","text":"old"}]}`), nil
	})}}
	cfg := &Config{LastSeen: map[string]string{}, ReadAppMentionsOnly: boolPtr(false), BotUserID: "UBOT"}

	unreads, err := findUnreadConversations(context.Background(), client, []conversation{{ID: "C123", Name: "general", IsChannel: true}}, cfg)
	if err != nil {
		t.Fatalf("findUnreadConversations: %v", err)
	}
	if len(unreads) != 1 || len(unreads[0].Messages) != 1 {
		t.Fatalf("unreads = %+v, want one recent message", unreads)
	}
	if got := cfg.LastSeen["C123"]; got != "" {
		t.Fatalf("LastSeen = %q before printing, want unchanged", got)
	}
}

func TestFindUnreadConversationsSkipsUnreadableConversations(t *testing.T) {
	client := &SlackClient{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/conversations.history" {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		return jsonResponse(`{"ok":false,"error":"not_in_channel"}`), nil
	})}}
	cfg := &Config{LastSeen: map[string]string{}, ReadAppMentionsOnly: boolPtr(false)}

	unreads, err := findUnreadConversations(context.Background(), client, []conversation{{ID: "C123", Name: "social", IsChannel: true}}, cfg)
	if err != nil {
		t.Fatalf("findUnreadConversations: %v", err)
	}
	if len(unreads) != 0 {
		t.Fatalf("unreads len = %d, want 0", len(unreads))
	}
}

func TestFindUnreadConversationsUsesOnlyLastSeenToFetchNewerMessages(t *testing.T) {
	client := &SlackClient{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/conversations.history" {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		if got := req.URL.Query().Get("oldest"); got != "1778108731.000100" {
			t.Fatalf("oldest = %q, want saved baseline", got)
		}
		return jsonResponse(`{"ok":true,"messages":[{"type":"message","ts":"1778108731.000200","user":"U1","text":"new"}]}`), nil
	})}}
	cfg := &Config{LastSeen: map[string]string{"C123": "1778108731.000100"}, ReadAppMentionsOnly: boolPtr(false), BotUserID: "UBOT"}

	unreads, err := findUnreadConversations(context.Background(), client, []conversation{{ID: "C123", Name: "general", IsChannel: true}}, cfg)
	if err != nil {
		t.Fatalf("findUnreadConversations: %v", err)
	}
	if len(unreads) != 1 || len(unreads[0].Messages) != 1 {
		t.Fatalf("unreads = %+v, want one message", unreads)
	}
	if got := unreads[0].Messages[0].Text; got != "new" {
		t.Fatalf("message text = %q, want new", got)
	}
}
