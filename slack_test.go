package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
