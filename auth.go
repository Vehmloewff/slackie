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
	ClientID        string            `json:"client_id"`
	RedirectURI     string            `json:"redirect_uri"`
	Scopes          []string          `json:"scopes"`
	BotScopes       []string          `json:"bot_scopes,omitempty"`
	AccessToken     string            `json:"access_token"` // legacy/user token fallback
	UserAccessToken string            `json:"user_access_token,omitempty"`
	BotAccessToken  string            `json:"bot_access_token,omitempty"`
	RefreshToken    string            `json:"refresh_token,omitempty"`
	TokenType       string            `json:"token_type,omitempty"`
	UserID          string            `json:"user_id,omitempty"`
	BotUserID       string            `json:"bot_user_id,omitempty"`
	TeamID          string            `json:"team_id,omitempty"`
	TeamName        string            `json:"team_name,omitempty"`
	AppToken        string            `json:"app_token,omitempty"`
	LastSeen        map[string]string `json:"last_seen,omitempty"`
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
	manifest, err := slackAppManifest(botName)
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
		Scopes:         append([]string(nil), defaultScopes...),
		BotScopes:      append([]string(nil), defaultBotScopes...),
		AccessToken:    botToken,
		BotAccessToken: botToken,
		AppToken:       appToken,
		LastSeen:       map[string]string{},
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
