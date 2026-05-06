package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSetupCommandPromptsForTokensAndSavesConfig(t *testing.T) {
	setupTempConfig(t)
	oldArgs := os.Args
	oldStdin := os.Stdin
	oldTestSlackToken := testSlackToken
	t.Cleanup(func() {
		os.Args = oldArgs
		os.Stdin = oldStdin
		testSlackToken = oldTestSlackToken
	})
	testSlackToken = func(ctx context.Context, httpClient *http.Client, token string) (*authTestResponse, error) {
		if token != "xoxb-command-token" {
			t.Fatalf("unexpected token %q", token)
		}
		return &authTestResponse{UserID: "U456", TeamID: "T456", Team: "Command"}, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("Command Bot\n\n\nxoxb-command-token\nxapp-command\n"); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	os.Stdin = r
	os.Args = []string{"slackie", "setup"}

	if err := run(); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunSetupPromptsForTokensAndSavesConfig(t *testing.T) {
	setupTempConfig(t)
	oldTestSlackToken := testSlackToken
	t.Cleanup(func() { testSlackToken = oldTestSlackToken })
	testSlackToken = func(ctx context.Context, httpClient *http.Client, token string) (*authTestResponse, error) {
		if token != "xoxb-test-token" {
			t.Fatalf("unexpected token %q", token)
		}
		return &authTestResponse{UserID: "U123", TeamID: "T123", Team: "Example"}, nil
	}

	if err := runSetup(strings.NewReader("Test Bot\n\n\nxoxb-test-token\nxapp-test\n")); err != nil {
		t.Fatalf("runSetup returned error: %v", err)
	}

	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.BotAccessToken != "xoxb-test-token" || cfg.AccessToken != "xoxb-test-token" {
		t.Fatalf("bot token was not saved correctly: %+v", cfg)
	}
	if cfg.AppToken != "xapp-test" {
		t.Fatalf("app token was not saved: %+v", cfg)
	}
	if cfg.BotUserID != "U123" || cfg.TeamID != "T123" || cfg.TeamName != "Example" {
		t.Fatalf("auth.test metadata was not saved: %+v", cfg)
	}
}

func TestRunSetupRejectsNonBotToken(t *testing.T) {
	setupTempConfig(t)
	if err := runSetup(strings.NewReader("Test Bot\n\n\nxoxp-not-a-bot\nxapp-test\n")); err == nil || !strings.Contains(err.Error(), "xoxb-") {
		t.Fatalf("expected xoxb validation error, got %v", err)
	}
}

func TestRunSetupRejectsNonAppToken(t *testing.T) {
	setupTempConfig(t)
	if err := runSetup(strings.NewReader("Test Bot\n\n\nxoxb-test\nxoxp-not-an-app\n")); err == nil || !strings.Contains(err.Error(), "xapp-") {
		t.Fatalf("expected xapp validation error, got %v", err)
	}
}

func TestSlackAppManifestIncludesChosenBotName(t *testing.T) {
	manifest, err := slackAppManifest("My Test Bot")
	if err != nil {
		t.Fatalf("slackAppManifest returned error: %v", err)
	}
	if !strings.Contains(manifest, `"name": "My Test Bot"`) || !strings.Contains(manifest, `"display_name": "My Test Bot"`) {
		t.Fatalf("manifest did not include chosen bot name:\n%s", manifest)
	}
	if !strings.Contains(manifest, `"socket_mode_enabled": true`) || !strings.Contains(manifest, `"app_mention"`) {
		t.Fatalf("manifest is missing expected Socket Mode settings/events:\n%s", manifest)
	}
}

func TestRunRejectsOldAuthCommand(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"slackie", "auth"}

	if err := run(); err == nil || !strings.Contains(err.Error(), "unknown command: auth") {
		t.Fatalf("expected old auth command to be unknown, got %v", err)
	}
}

func setupTempConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
}
