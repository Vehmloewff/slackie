package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	configDirName  = "slackie"
	configFileName = "config.json"
)

// Config is the small durable JSON state stored in the user config dir.
// It intentionally keeps only the values needed after setup completes.
type Config struct {
	ClientID            string   `json:"client_id"`
	RedirectURI         string   `json:"redirect_uri"`
	Scopes              []string `json:"scopes"`
	BotScopes           []string `json:"bot_scopes,omitempty"`
	AccessToken         string   `json:"access_token"` // legacy/user token fallback
	UserAccessToken     string   `json:"user_access_token,omitempty"`
	BotAccessToken      string   `json:"bot_access_token,omitempty"`
	RefreshToken        string   `json:"refresh_token,omitempty"`
	TokenType           string   `json:"token_type,omitempty"`
	UserID              string   `json:"user_id,omitempty"`
	BotUserID           string   `json:"bot_user_id,omitempty"`
	TeamID              string   `json:"team_id,omitempty"`
	TeamName            string   `json:"team_name,omitempty"`
	AppToken            string   `json:"app_token,omitempty"`
	LastSeen            string   `json:"last_seen,omitempty"`
	ReadAppMentionsOnly *bool    `json:"read_app_mentions_only,omitempty"`
	ReadDMs             *bool    `json:"read_dms,omitempty"`
	ReadPrivateChannels *bool    `json:"read_private_channels,omitempty"`
	ReadMessageGroups   *bool    `json:"read_message_groups,omitempty"`
}

func (cfg *Config) UnmarshalJSON(data []byte) error {
	type configCompat struct {
		ClientID            string          `json:"client_id"`
		RedirectURI         string          `json:"redirect_uri"`
		Scopes              []string        `json:"scopes"`
		BotScopes           []string        `json:"bot_scopes,omitempty"`
		AccessToken         string          `json:"access_token"`
		UserAccessToken     string          `json:"user_access_token,omitempty"`
		BotAccessToken      string          `json:"bot_access_token,omitempty"`
		RefreshToken        string          `json:"refresh_token,omitempty"`
		TokenType           string          `json:"token_type,omitempty"`
		UserID              string          `json:"user_id,omitempty"`
		BotUserID           string          `json:"bot_user_id,omitempty"`
		TeamID              string          `json:"team_id,omitempty"`
		TeamName            string          `json:"team_name,omitempty"`
		AppToken            string          `json:"app_token,omitempty"`
		LastSeen            json.RawMessage `json:"last_seen,omitempty"`
		ReadAppMentionsOnly *bool           `json:"read_app_mentions_only,omitempty"`
		ReadDMs             *bool           `json:"read_dms,omitempty"`
		ReadPrivateChannels *bool           `json:"read_private_channels,omitempty"`
		ReadMessageGroups   *bool           `json:"read_message_groups,omitempty"`
	}

	var compat configCompat
	if err := json.Unmarshal(data, &compat); err != nil {
		return err
	}

	*cfg = Config{
		ClientID:            compat.ClientID,
		RedirectURI:         compat.RedirectURI,
		Scopes:              compat.Scopes,
		BotScopes:           compat.BotScopes,
		AccessToken:         compat.AccessToken,
		UserAccessToken:     compat.UserAccessToken,
		BotAccessToken:      compat.BotAccessToken,
		RefreshToken:        compat.RefreshToken,
		TokenType:           compat.TokenType,
		UserID:              compat.UserID,
		BotUserID:           compat.BotUserID,
		TeamID:              compat.TeamID,
		TeamName:            compat.TeamName,
		AppToken:            compat.AppToken,
		ReadAppMentionsOnly: compat.ReadAppMentionsOnly,
		ReadDMs:             compat.ReadDMs,
		ReadPrivateChannels: compat.ReadPrivateChannels,
		ReadMessageGroups:   compat.ReadMessageGroups,
	}
	return cfg.unmarshalLastSeen(compat.LastSeen)
}

func (cfg *Config) unmarshalLastSeen(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var lastSeen string
	if err := json.Unmarshal(raw, &lastSeen); err == nil {
		cfg.LastSeen = strings.TrimSpace(lastSeen)
		return nil
	}

	var legacy map[string]string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return err
	}
	for _, ts := range legacy {
		ts = strings.TrimSpace(ts)
		if isSlackTS(ts) && slackTSGreater(ts, cfg.LastSeen) {
			cfg.LastSeen = ts
		}
	}
	return nil
}

type authTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
	URL    string `json:"url"`
}

var testSlackToken = func(ctx context.Context, httpClient *http.Client, token string) (*authTestResponse, error) {
	client := &SlackClient{HTTPClient: httpClient, Token: token}
	return client.authTest(ctx)
}

func cmdSetup() error {
	return runSetup(os.Stdin)
}

func runSetup(reader io.Reader) error {
	fmt.Println("Set up slackie by creating a Slack app from a manifest, then pasting its tokens.")
	fmt.Println()

	buf := bufio.NewReader(reader)
	fmt.Print("Bot name to use in Slack: ")
	botName, err := readSetupToken(buf, "bot name")
	if err != nil {
		return err
	}
	readAppMentionsOnly, err := promptReadAppMentionsOnly(buf)
	if err != nil {
		return err
	}
	readDMs, err := promptSetupYesNo(buf, "Allow slackie to receive DMs?", "If enabled, slackie can read direct messages sent to the app.")
	if err != nil {
		return err
	}
	readPrivateChannels, err := promptSetupYesNo(buf, "Allow slackie to receive messages in private channels?", "If enabled, slackie can read messages from private channels where the app is installed.")
	if err != nil {
		return err
	}
	readMessageGroups, err := promptSetupYesNo(buf, "Allow slackie to receive messages in message groups?", "If enabled, slackie can read multi-person direct messages where the app is installed.")
	if err != nil {
		return err
	}
	settings := readSettings{ReadAppMentionsOnly: readAppMentionsOnly, ReadDMs: readDMs, ReadPrivateChannels: readPrivateChannels, ReadMessageGroups: readMessageGroups}
	manifest, err := slackAppManifest(botName, settings)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Create the Slack app:")
	fmt.Println("1. Open https://api.slack.com/apps and choose \"Create New App\".")
	fmt.Println("2. Choose \"From an app manifest\".")
	fmt.Println("3. Pick the workspace you want slackie to send Slack messages to and read messages from.")
	fmt.Println()
	if err := waitForEnter(buf, "Press Enter to show the Slack app manifest: "); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Slack app manifest:")
	fmt.Println("```json")
	fmt.Println(manifest)
	fmt.Println("```")
	fmt.Println()
	if err := waitForEnter(buf, "Copy the manifest above. Press Enter to continue: "); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Paste the manifest into Slack and create the app. Then click Install App, click Install to your workspace, click Allow, and copy the Bot User OAuth Token (xoxb-...) that Slack shows you.")
	fmt.Print("Bot User OAuth Token (xoxb-...): ")
	botToken, err := readSetupToken(buf, "bot token")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(botToken, "xoxb-") {
		return errors.New("invalid bot token: expected a Bot User OAuth Token starting with xoxb-")
	}

	fmt.Println()
	fmt.Println("In Basic Information > App-Level Tokens, create a token with connections:write and copy the xapp-... token.")
	fmt.Print("App-level Socket Mode token (xapp-...): ")
	appToken, err := readSetupToken(buf, "app token")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(appToken, "xapp-") {
		return errors.New("invalid app token: expected an app-level Socket Mode token starting with xapp-")
	}
	cfg := Config{
		Scopes:              append([]string(nil), defaultScopes...),
		BotScopes:           setupBotScopes(settings),
		AccessToken:         botToken,
		BotAccessToken:      botToken,
		AppToken:            appToken,
		LastSeen:            slackTimestamp(time.Now()),
		ReadAppMentionsOnly: boolPtr(readAppMentionsOnly),
		ReadDMs:             boolPtr(readDMs),
		ReadPrivateChannels: boolPtr(readPrivateChannels),
		ReadMessageGroups:   boolPtr(readMessageGroups),
	}

	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	authInfo, err := testSlackToken(ctx, httpClient, botToken)
	if err != nil {
		return fmt.Errorf("Slack rejected the bot token or it could not be verified; token was not saved: %w", err)
	}
	cfg.BotUserID = authInfo.UserID
	cfg.TeamID = authInfo.TeamID
	cfg.TeamName = authInfo.Team

	path, err := saveConfig(cfg)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Setup complete. Bot token saved.")
	fmt.Println("slackie will use this Bot User OAuth Token to read Slack messages and send app messages.")
	if cfg.readAppMentionsOnly() {
		fmt.Println("Read mode: mention-only (channel messages are shown only when they mention the bot).")
	} else {
		fmt.Println("Read mode: all-channel-messages (messages are shown from channels the app is installed in and subscribed to).")
	}
	fmt.Printf("DMs: %s; private channels: %s; message groups: %s\n", enabledLabel(cfg.readDMs()), enabledLabel(cfg.readPrivateChannels()), enabledLabel(cfg.readMessageGroups()))
	if cfg.TeamName != "" || cfg.TeamID != "" {
		fmt.Printf("Workspace: %s (%s)\n", emptyFallback(cfg.TeamName, "unknown"), emptyFallback(cfg.TeamID, "unknown"))
	}
	if cfg.BotUserID != "" {
		fmt.Printf("Bot user ID: %s\n", cfg.BotUserID)
	}
	fmt.Println("Socket Mode app token saved for slackie read --wait.")
	fmt.Println("Next: try `slackie read`, `slackie read --wait`, or pipe a message to `slackie send <target>`.")
	fmt.Printf("Config: %s\n", path)
	return nil
}

func promptReadAppMentionsOnly(reader *bufio.Reader) (bool, error) {
	fmt.Println()
	fmt.Println("Should the app receive only messages where the bot is mentioned, or all messages from the channels the app has access to?")
	fmt.Println("1. Mention-only mode")
	fmt.Println("   slackie receives channel messages only when someone mentions the bot, such as <@bot>.")
	fmt.Println("2. All-channel-messages mode")
	fmt.Println("   slackie receives all messages from public channels the app is installed in; DMs, private channels, and message groups are configured next.")
	for {
		fmt.Print("Choose read mode [1/2]: ")
		choice, err := readSetupToken(reader, "read mode")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(choice) {
		case "1", "mention", "mention-only", "mention-only mode":
			return true, nil
		case "2", "all", "all-channel", "all-channel-messages", "all-channel-messages mode":
			return false, nil
		default:
			fmt.Println("Please enter 1 for mention-only mode or 2 for all-channel-messages mode.")
		}
	}
}

func promptSetupYesNo(reader *bufio.Reader, question, explanation string) (bool, error) {
	fmt.Println()
	fmt.Println(question)
	fmt.Println(explanation)
	for {
		fmt.Print("Choose [y/n]: ")
		choice, err := readSetupToken(reader, "choice")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(choice) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Println("Please enter y or n.")
		}
	}
}

func setupBotScopes(settings readSettings) []string {
	return manifestBotScopes(settings)
}

func boolPtr(v bool) *bool {
	return &v
}

func (cfg Config) readAppMentionsOnly() bool {
	if cfg.ReadAppMentionsOnly == nil {
		return true
	}
	return *cfg.ReadAppMentionsOnly
}

func (cfg Config) readDMs() bool {
	if cfg.ReadDMs == nil {
		return true
	}
	return *cfg.ReadDMs
}

func (cfg Config) readPrivateChannels() bool {
	if cfg.ReadPrivateChannels == nil {
		return true
	}
	return *cfg.ReadPrivateChannels
}

func (cfg Config) readMessageGroups() bool {
	if cfg.ReadMessageGroups == nil {
		return true
	}
	return *cfg.ReadMessageGroups
}

func enabledLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func configReadSettings(cfg Config) readSettings {
	return readSettings{
		ReadAppMentionsOnly: cfg.readAppMentionsOnly(),
		ReadDMs:             cfg.readDMs(),
		ReadPrivateChannels: cfg.readPrivateChannels(),
		ReadMessageGroups:   cfg.readMessageGroups(),
	}
}

func readSetupToken(reader *bufio.Reader, name string) (string, error) {
	pasted, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	token := strings.TrimSpace(pasted)
	if token == "" {
		return "", fmt.Errorf("no %s provided", name)
	}
	return token, nil
}

func waitForEnter(reader *bufio.Reader, prompt string) error {
	fmt.Print(prompt)
	_, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("wait for Enter: %w", err)
	}
	return nil
}

func loadConfig() (Config, string, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, path, fmt.Errorf("no config found at %s; run: slackie setup", path)
		}
		return Config{}, path, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, path, fmt.Errorf("parse config %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.AccessToken) == "" && strings.TrimSpace(cfg.BotAccessToken) == "" && strings.TrimSpace(cfg.UserAccessToken) == "" {
		return Config{}, path, fmt.Errorf("config %s does not contain an access token; run: slackie setup", path)
	}
	return cfg, path, nil
}

func readAccessToken(cfg Config) string {
	if token := strings.TrimSpace(cfg.BotAccessToken); token != "" {
		return token
	}
	if token := strings.TrimSpace(cfg.AccessToken); token != "" {
		return token
	}
	return strings.TrimSpace(cfg.UserAccessToken)
}

func writeAccessToken(cfg Config) string {
	if token := strings.TrimSpace(cfg.BotAccessToken); token != "" {
		return token
	}
	if token := strings.TrimSpace(cfg.AccessToken); token != "" {
		return token
	}
	return strings.TrimSpace(cfg.UserAccessToken)
}

func mentionUserID(cfg Config) string {
	if id := strings.TrimSpace(cfg.BotUserID); id != "" {
		return id
	}
	return strings.TrimSpace(cfg.UserID)
}

func saveConfig(cfg Config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return path, nil
}

func cmdReset() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("No slackie config found at %s\n", path)
			return nil
		}
		return fmt.Errorf("remove config: %w", err)
	}
	fmt.Printf("Removed slackie config: %s\n", path)
	return nil
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, configDirName, configFileName), nil
}
