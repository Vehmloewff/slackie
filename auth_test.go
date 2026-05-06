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
	if _, err := w.WriteString("Command Bot\n1\ny\ny\ny\n\n\nxoxb-command-token\nxapp-command\n"); err != nil {
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

	if err := runSetup(strings.NewReader("Test Bot\n1\ny\ny\ny\n\n\nxoxb-test-token\nxapp-test\n")); err != nil {
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
	if cfg.ReadAppMentionsOnly == nil || !*cfg.ReadAppMentionsOnly {
		t.Fatalf("read_app_mentions_only was not persisted as true: %+v", cfg)
	}
	if cfg.ReadDMs == nil || !*cfg.ReadDMs || cfg.ReadPrivateChannels == nil || !*cfg.ReadPrivateChannels || cfg.ReadMessageGroups == nil || !*cfg.ReadMessageGroups {
		t.Fatalf("read access settings were not persisted as true: %+v", cfg)
	}
}

func TestRunSetupSelectsAllChannelMessagesAndSavesConfig(t *testing.T) {
	setupTempConfig(t)
	oldTestSlackToken := testSlackToken
	t.Cleanup(func() { testSlackToken = oldTestSlackToken })
	testSlackToken = func(ctx context.Context, httpClient *http.Client, token string) (*authTestResponse, error) {
		return &authTestResponse{UserID: "U789", TeamID: "T789", Team: "All Messages"}, nil
	}

	if err := runSetup(strings.NewReader("All Bot\n2\nn\ny\nn\n\n\nxoxb-all-token\nxapp-all\n")); err != nil {
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
	if cfg.ReadAppMentionsOnly == nil || *cfg.ReadAppMentionsOnly {
		t.Fatalf("read_app_mentions_only was not persisted as false: %+v", cfg)
	}
	if cfg.ReadDMs == nil || *cfg.ReadDMs || cfg.ReadPrivateChannels == nil || !*cfg.ReadPrivateChannels || cfg.ReadMessageGroups == nil || *cfg.ReadMessageGroups {
		t.Fatalf("read access settings were not persisted correctly: %+v", cfg)
	}
}

func TestMissingReadModeDefaultsToMentionOnly(t *testing.T) {
	cfg := Config{}
	if !cfg.readAppMentionsOnly() {
		t.Fatal("missing read_app_mentions_only should default to mention-only")
	}
	if !cfg.readDMs() || !cfg.readPrivateChannels() || !cfg.readMessageGroups() {
		t.Fatal("missing read access settings should default to enabled for existing configs")
	}
}

func TestRunSetupRejectsNonBotToken(t *testing.T) {
	setupTempConfig(t)
	if err := runSetup(strings.NewReader("Test Bot\n1\ny\ny\ny\n\n\nxoxp-not-a-bot\nxapp-test\n")); err == nil || !strings.Contains(err.Error(), "xoxb-") {
		t.Fatalf("expected xoxb validation error, got %v", err)
	}
}

func TestRunSetupRejectsNonAppToken(t *testing.T) {
	setupTempConfig(t)
	if err := runSetup(strings.NewReader("Test Bot\n1\ny\ny\ny\n\n\nxoxb-test\nxoxp-not-an-app\n")); err == nil || !strings.Contains(err.Error(), "xapp-") {
		t.Fatalf("expected xapp validation error, got %v", err)
	}
}

func TestSlackAppManifestIncludesChosenBotName(t *testing.T) {
	manifest, err := slackAppManifest("My Test Bot", readSettings{ReadAppMentionsOnly: true, ReadDMs: true, ReadPrivateChannels: true, ReadMessageGroups: true})
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

func TestSlackAppManifestMentionOnlyMode(t *testing.T) {
	manifest, err := slackAppManifest("Mention Bot", readSettings{ReadAppMentionsOnly: true})
	if err != nil {
		t.Fatalf("slackAppManifest returned error: %v", err)
	}
	if !strings.Contains(manifest, `"app_mention"`) {
		t.Fatalf("mention-only manifest missing app_mention event:\n%s", manifest)
	}
	for _, unexpected := range []string{`"message.channels"`, `"message.groups"`, `"message.im"`, `"message.mpim"`} {
		if strings.Contains(manifest, unexpected) {
			t.Fatalf("mention-only manifest unexpectedly contains %s:\n%s", unexpected, manifest)
		}
	}
}

func TestSlackAppManifestAllChannelMessagesMode(t *testing.T) {
	manifest, err := slackAppManifest("All Bot", readSettings{ReadAppMentionsOnly: false, ReadDMs: true, ReadPrivateChannels: true, ReadMessageGroups: true})
	if err != nil {
		t.Fatalf("slackAppManifest returned error: %v", err)
	}
	for _, expected := range []string{`"message.channels"`, `"message.groups"`, `"message.im"`, `"message.mpim"`, `"channels:history"`, `"groups:history"`, `"im:history"`, `"mpim:history"`} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("all-channel-messages manifest missing %s:\n%s", expected, manifest)
		}
	}
	if strings.Contains(manifest, `"app_mention"`) {
		t.Fatalf("all-channel-messages manifest should use message events instead of app_mention:\n%s", manifest)
	}
}

func TestSlackAppManifestEnablesAppHomeMessagesForDMs(t *testing.T) {
	manifest, err := slackAppManifest("DM Bot", readSettings{ReadDMs: true})
	if err != nil {
		t.Fatalf("slackAppManifest returned error: %v", err)
	}
	for _, expected := range []string{`"app_home"`, `"messages_tab_enabled": true`, `"messages_tab_read_only_enabled": false`} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("DM manifest missing %s:\n%s", expected, manifest)
		}
	}
}

func TestSlackAppManifestOmitsAppHomeMessagesWhenDMsDisabled(t *testing.T) {
	manifest, err := slackAppManifest("No DM Bot", readSettings{ReadDMs: false})
	if err != nil {
		t.Fatalf("slackAppManifest returned error: %v", err)
	}
	if strings.Contains(manifest, `"app_home"`) || strings.Contains(manifest, `"messages_tab_enabled"`) {
		t.Fatalf("non-DM manifest unexpectedly contains app home messages config:\n%s", manifest)
	}
}

func TestSlackAppManifestOmitsDisabledReadAccess(t *testing.T) {
	manifest, err := slackAppManifest("Public Only Bot", readSettings{ReadAppMentionsOnly: false})
	if err != nil {
		t.Fatalf("slackAppManifest returned error: %v", err)
	}
	if !strings.Contains(manifest, `"message.channels"`) {
		t.Fatalf("public-channel manifest missing message.channels:\n%s", manifest)
	}
	for _, unexpected := range []string{`"message.groups"`, `"message.im"`, `"message.mpim"`, `"groups:history"`, `"im:history"`, `"mpim:history"`} {
		if strings.Contains(manifest, unexpected) {
			t.Fatalf("manifest unexpectedly contains disabled access %s:\n%s", unexpected, manifest)
		}
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
