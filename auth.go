package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	slackAuthorizeURL  = "https://slack.com/oauth/v2/authorize"
	slackOAuthTokenURL = "https://slack.com/api/oauth.v2.access"
	configDirName      = "slackie"
	configFileName     = "config.json"
)

// Config is the small durable JSON state stored in the user config dir.
// It intentionally keeps only the values needed after auth completes.
type Config struct {
	ClientID     string   `json:"client_id"`
	RedirectURI  string   `json:"redirect_uri"`
	Scopes       []string `json:"scopes"`
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	UserID       string   `json:"user_id,omitempty"`
	TeamID       string   `json:"team_id,omitempty"`
	TeamName     string   `json:"team_name,omitempty"`
}

type oauthAccessResponse struct {
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Team         struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	AuthedUser struct {
		ID           string `json:"id"`
		Scope        string `json:"scope"`
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
	} `json:"authed_user"`
}

type authTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
	URL    string `json:"url"`
}

func cmdAuth() error {
	clientID := strings.TrimSpace(slackClientID)
	redirectURI := strings.TrimSpace(slackRedirectURI)

	state, err := randomURLSafe(32)
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return fmt.Errorf("generate code verifier: %w", err)
	}
	challenge := pkceChallengeS256(verifier)

	authURL, err := buildAuthorizeURL(clientID, redirectURI, state, challenge, defaultScopes)
	if err != nil {
		return err
	}

	fmt.Println("Open this Slack authorize URL in your browser:")
	fmt.Println()
	fmt.Println(authURL)
	fmt.Println()
	fmt.Println("After you approve access, Slack will redirect to your configured redirect URI.")
	fmt.Println("Copy the full final redirected URL from your browser address bar and paste it below.")
	fmt.Println("The auth code is short-lived, so do this promptly.")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Paste the full redirected URL here: ")
	pasted, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read pasted URL: %w", err)
	}
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return errors.New("no callback URL provided")
	}

	code, returnedState, err := parseOAuthCallback(pasted)
	if err != nil {
		return err
	}
	if returnedState != state {
		return errors.New("oauth state mismatch")
	}

	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := exchangeOAuthCode(ctx, httpClient, clientID, redirectURI, code, verifier)
	if err != nil {
		return err
	}

	accessToken := strings.TrimSpace(resp.AuthedUser.AccessToken)
	if accessToken == "" {
		accessToken = strings.TrimSpace(resp.AccessToken)
	}
	if accessToken == "" {
		return errors.New("oauth exchange succeeded but no user access token was returned")
	}

	tokenType := strings.TrimSpace(resp.AuthedUser.TokenType)
	if tokenType == "" {
		tokenType = strings.TrimSpace(resp.TokenType)
	}
	refreshToken := strings.TrimSpace(resp.AuthedUser.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(resp.RefreshToken)
	}

	cfg := Config{
		ClientID:     clientID,
		RedirectURI:  redirectURI,
		Scopes:       append([]string(nil), defaultScopes...),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		UserID:       strings.TrimSpace(resp.AuthedUser.ID),
		TeamID:       strings.TrimSpace(resp.Team.ID),
		TeamName:     strings.TrimSpace(resp.Team.Name),
	}

	client := &SlackClient{HTTPClient: httpClient, Token: cfg.AccessToken}
	authInfo, err := client.authTest(ctx)
	if err == nil {
		if cfg.UserID == "" {
			cfg.UserID = authInfo.UserID
		}
		if cfg.TeamID == "" {
			cfg.TeamID = authInfo.TeamID
		}
		if cfg.TeamName == "" {
			cfg.TeamName = authInfo.Team
		}
	}

	path, err := saveConfig(cfg)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Auth saved.")
	if cfg.TeamName != "" || cfg.TeamID != "" {
		fmt.Printf("Workspace: %s (%s)\n", emptyFallback(cfg.TeamName, "unknown"), emptyFallback(cfg.TeamID, "unknown"))
	}
	if cfg.UserID != "" {
		fmt.Printf("User ID: %s\n", cfg.UserID)
	}
	fmt.Printf("Config: %s\n", path)
	return nil
}

func buildAuthorizeURL(clientID, redirectURI, state, challenge string, scopes []string) (string, error) {
	u, err := url.Parse(slackAuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize url: %w", err)
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("user_scope", strings.Join(scopes, ","))
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseOAuthCallback(raw string) (code, state string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("malformed callback url: %w", err)
	}
	q := u.Query()
	if oauthErr := strings.TrimSpace(q.Get("error")); oauthErr != "" {
		if oauthErr == "access_denied" {
			return "", "", errors.New("access was denied in Slack")
		}
		return "", "", fmt.Errorf("oauth error from Slack: %s", oauthErr)
	}
	state = strings.TrimSpace(q.Get("state"))
	if state == "" {
		return "", "", errors.New("callback URL is missing state")
	}
	code = strings.TrimSpace(q.Get("code"))
	if code == "" {
		return "", "", errors.New("callback URL is missing code")
	}
	return code, state, nil
}

func exchangeOAuthCode(ctx context.Context, httpClient *http.Client, clientID, redirectURI, code, verifier string) (*oauthAccessResponse, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build oauth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("oauth exchange failed: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out oauthAccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode oauth response: %w", err)
	}
	if !out.OK {
		return nil, fmt.Errorf("oauth exchange failed: %s", emptyFallback(out.Error, "unknown_error"))
	}
	return &out, nil
}

func loadConfig() (Config, string, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, path, fmt.Errorf("no config found at %s; run: slackie auth", path)
		}
		return Config{}, path, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, path, fmt.Errorf("parse config %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.AccessToken) == "" {
		return Config{}, path, fmt.Errorf("config %s does not contain an access token; run: slackie auth", path)
	}
	return cfg, path, nil
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

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, configDirName, configFileName), nil
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
