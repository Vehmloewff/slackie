package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPostMessageMrkdwn(t *testing.T) {
	tests := []struct {
		name       string
		mrkdwn     bool
		wantMrkdwn string
	}{
		{name: "enabled", mrkdwn: true, wantMrkdwn: "true"},
		{name: "disabled", mrkdwn: false, wantMrkdwn: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &SlackClient{
				Token: "xoxb-test",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.String() != slackAPIBase+"chat.postMessage" {
						t.Fatalf("unexpected URL: %s", req.URL.String())
					}
					body, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatalf("read body: %v", err)
					}
					form, err := url.ParseQuery(string(body))
					if err != nil {
						t.Fatalf("parse form: %v", err)
					}
					if got := form.Get("mrkdwn"); got != tt.wantMrkdwn {
						t.Fatalf("mrkdwn = %q, want %q", got, tt.wantMrkdwn)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"ok":true,"channel":"C123","ts":"1.23"}`)),
					}, nil
				})},
			}

			if _, err := client.postMessage(context.Background(), "C123", "*hello*", "", tt.mrkdwn); err != nil {
				t.Fatalf("postMessage returned error: %v", err)
			}
		})
	}
}

func TestFetchConversationHistoryBacksOffOnRateLimit(t *testing.T) {
	oldSleep := historyRateLimitSleep
	t.Cleanup(func() { historyRateLimitSleep = oldSleep })

	var delays []time.Duration
	historyRateLimitSleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}

	requests := 0
	client := &SlackClient{
		Token: "xoxb-test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if req.URL.Path != "/api/conversations.history" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if requests <= 2 {
				header := make(http.Header)
				header.Set("Retry-After", "3")
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error":"ratelimited"}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"messages":[{"type":"message","text":"hello","ts":"1.23"}]}`)),
			}, nil
		})},
	}

	messages, err := fetchConversationHistory(context.Background(), client, "C123", HistoryOptions{})
	if err != nil {
		t.Fatalf("fetchConversationHistory returned error: %v", err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
	wantDelays := []time.Duration{3 * time.Second, 3 * time.Second}
	if len(delays) != len(wantDelays) {
		t.Fatalf("delays = %v, want %v", delays, wantDelays)
	}
	for i := range wantDelays {
		if delays[i] != wantDelays[i] {
			t.Fatalf("delays = %v, want %v", delays, wantDelays)
		}
	}
	if len(messages) != 1 || messages[0].Text != "hello" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestFetchConversationHistoryUsesExponentialBackoffWhenRetryAfterIsShort(t *testing.T) {
	oldSleep := historyRateLimitSleep
	t.Cleanup(func() { historyRateLimitSleep = oldSleep })

	var delays []time.Duration
	historyRateLimitSleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}

	requests := 0
	client := &SlackClient{
		Token: "xoxb-test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if requests <= 3 {
				header := make(http.Header)
				header.Set("Retry-After", "1")
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error":"ratelimited"}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"messages":[]}`)),
			}, nil
		})},
	}

	if _, err := fetchConversationHistory(context.Background(), client, "C123", HistoryOptions{}); err != nil {
		t.Fatalf("fetchConversationHistory returned error: %v", err)
	}
	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(delays) != len(wantDelays) {
		t.Fatalf("delays = %v, want %v", delays, wantDelays)
	}
	for i := range wantDelays {
		if delays[i] != wantDelays[i] {
			t.Fatalf("delays = %v, want %v", delays, wantDelays)
		}
	}
}

func TestParseSendArgsNoMrkdwn(t *testing.T) {
	target, attachments, mrkdwn, err := parseSendArgs([]string{"--no-mrkdwn", "--attach", "file.txt", "#general"})
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if target != "#general" || len(attachments) != 1 || attachments[0] != "file.txt" || mrkdwn {
		t.Fatalf("parseSendArgs = target %q, attachments %v, mrkdwn %v", target, attachments, mrkdwn)
	}
}

func TestThreadingTargetUsesReadableDisplay(t *testing.T) {
	got := threadingTarget("#backend", "1778108731.796869", "")
	want := "#backend:1778108731.796869"
	if got != want {
		t.Fatalf("threadingTarget = %q, want %q", got, want)
	}
}

func TestMessageThreadingTargetUsesConversationTitle(t *testing.T) {
	conv := conversation{ID: "C0ATV4VEDC4", Name: "backend", IsChannel: true}
	msg := message{TS: "1778108731.796869"}
	got := messageThreadingTarget(conv, msg, nil)
	want := "#backend:1778108731.796869"
	if got != want {
		t.Fatalf("messageThreadingTarget = %q, want %q", got, want)
	}
}

func TestReadableUserMentions(t *testing.T) {
	got := readableUserMentions("hi <@U123> and <@W456|old>", map[string]string{
		"U123": "alice",
		"W456": "Alice Baker",
	})
	want := "hi <@alice> and <@Alice Baker>"
	if got != want {
		t.Fatalf("readableUserMentions = %q, want %q", got, want)
	}
}

func TestResolveUserMentionNames(t *testing.T) {
	users := []user{{ID: "U123", Name: "alice"}, {ID: "U456", RealName: "Alice Baker"}}
	got, err := resolveUserMentionNames("hi <@alice> and <@Alice Baker> and <@U789>", users)
	if err != nil {
		t.Fatalf("resolveUserMentionNames returned error: %v", err)
	}
	want := "hi <@U123> and <@U456> and <@U789>"
	if got != want {
		t.Fatalf("resolveUserMentionNames = %q, want %q", got, want)
	}
}
