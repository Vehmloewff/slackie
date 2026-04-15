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
	"sort"
	"strings"
	"time"
)

const (
	slackAuthorizeURL  = "https://slack.com/oauth/v2/authorize"
	slackOAuthTokenURL = "https://slack.com/api/oauth.v2.access"
	slackAPIBase       = "https://slack.com/api/"
	configDirName      = "slackie"
	configFileName     = "config.json"
	slackClientID      = "10137959977282.10936753440897"
	slackRedirectURI   = "https://localhost:8998"
)

var defaultScopes = []string{
	"chat:write",
	"channels:history",
	"groups:history",
	"im:history",
	"mpim:history",
	"channels:read",
	"groups:read",
	"im:read",
	"mpim:read",
	"users:read",
}

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

type SlackClient struct {
	HTTPClient *http.Client
	Token      string
}

type slackErrorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
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

type conversationsListResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error"`
	Channels         []conversation `json:"channels"`
	ResponseMetadata responseMeta   `json:"response_metadata"`
}

type conversation struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	IsChannel          bool     `json:"is_channel"`
	IsGroup            bool     `json:"is_group"`
	IsIM               bool     `json:"is_im"`
	IsMPIM             bool     `json:"is_mpim"`
	IsPrivate          bool     `json:"is_private"`
	User               string   `json:"user"`
	LastRead           string   `json:"last_read"`
	UnreadCount        int      `json:"unread_count"`
	UnreadCountDisplay int      `json:"unread_count_display"`
	Latest             *message `json:"latest"`
}

type message struct {
	Type     string `json:"type"`
	User     string `json:"user"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
	Subtype  string `json:"subtype"`
	BotID    string `json:"bot_id"`
	Username string `json:"username"`
}

type conversationHistoryResponse struct {
	OK               bool         `json:"ok"`
	Error            string       `json:"error"`
	Messages         []message    `json:"messages"`
	HasMore          bool         `json:"has_more"`
	ResponseMetadata responseMeta `json:"response_metadata"`
}

type usersListResponse struct {
	OK               bool         `json:"ok"`
	Error            string       `json:"error"`
	Members          []user       `json:"members"`
	ResponseMetadata responseMeta `json:"response_metadata"`
}

type user struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name"`
	Deleted  bool   `json:"deleted"`
	IsBot    bool   `json:"is_bot"`
	Profile  struct {
		DisplayName       string `json:"display_name"`
		DisplayNameNormal string `json:"display_name_normalized"`
		RealName          string `json:"real_name"`
	} `json:"profile"`
}

type responseMeta struct {
	NextCursor string `json:"next_cursor"`
}

type conversationsOpenResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error"`
	Channel conversation `json:"channel"`
}

type chatPostMessageResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

type unreadConversation struct {
	Conv     conversation
	Messages []message
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
		return nil
	case "auth":
		if len(args) != 1 {
			return errors.New("usage: slackie auth")
		}
		return cmdAuth()
	case "unreads":
		if len(args) != 1 {
			return errors.New("usage: slackie unreads")
		}
		return cmdUnreads()
	case "send":
		if len(args) < 3 {
			return errors.New("usage: slackie send <target> <message>")
		}
		target := args[1]
		message := strings.Join(args[2:], " ")
		return cmdSend(target, message)
	default:
		printHelp()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printHelp() {
	fmt.Println("slackie - tiny Slack CLI")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  slackie auth")
	fmt.Println("  slackie unreads")
	fmt.Println("  slackie send <target> <message>")
	fmt.Println("  slackie help")
	fmt.Println()
	fmt.Println("Targets:")
	fmt.Println("  #channel-name")
	fmt.Println("  C12345678")
	fmt.Println("  @alice")
	fmt.Println("  #channel-name:1740000000.123456")
	fmt.Println("  C12345678:1740000000.123456")
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

func cmdUnreads() error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	client := &SlackClient{HTTPClient: &http.Client{Timeout: 30 * time.Second}, Token: cfg.AccessToken}
	ctx := context.Background()

	users, err := client.listUsers(ctx)
	if err != nil {
		return fmt.Errorf("load users: %w", err)
	}
	userNames := buildUserNameMap(users)

	convs, err := client.listAllConversations(ctx)
	if err != nil {
		return fmt.Errorf("load conversations: %w", err)
	}

	unreads, err := collectUnreadConversations(ctx, client, convs)
	if err != nil {
		return err
	}
	if len(unreads) == 0 {
		fmt.Println("No unread conversations.")
		return nil
	}

	for i, item := range unreads {
		printUnreadConversation(item, userNames, cfg.UserID)
		latestTS := latestMessageTS(item.Messages)
		if latestTS != "" {
			if err := client.markConversationRead(ctx, item.Conv.ID, latestTS); err != nil {
				fmt.Printf("mark read failed: %v\n", err)
			} else {
				fmt.Println("marked read")
			}
		}
		if i != len(unreads)-1 {
			fmt.Println()
		}
	}
	return nil
}

func cmdSend(target, text string) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	client := &SlackClient{HTTPClient: &http.Client{Timeout: 30 * time.Second}, Token: cfg.AccessToken}
	ctx := context.Background()

	convTarget, threadTS := splitThreadTarget(target)
	convID, display, err := resolveTarget(ctx, client, convTarget)
	if err != nil {
		return err
	}

	resp, err := client.postMessage(ctx, convID, text, threadTS)
	if err != nil {
		return err
	}

	if threadTS != "" {
		fmt.Printf("sent to %s thread %s (%s)\n", display, threadTS, resp.TS)
	} else {
		fmt.Printf("sent to %s (%s)\n", display, resp.TS)
	}
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

func emptyFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func (c *SlackClient) authTest(ctx context.Context) (*authTestResponse, error) {
	var out authTestResponse
	if err := c.apiForm(ctx, "auth.test", url.Values{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SlackClient) listAllConversations(ctx context.Context) ([]conversation, error) {
	var all []conversation
	cursor := ""
	for {
		params := url.Values{}
		params.Set("types", "public_channel,private_channel,im,mpim")
		params.Set("exclude_archived", "true")
		params.Set("limit", "999")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var out conversationsListResponse
		if err := c.apiGet(ctx, "conversations.list", params, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Channels...)
		cursor = strings.TrimSpace(out.ResponseMetadata.NextCursor)
		if cursor == "" {
			break
		}
	}
	return all, nil
}

func (c *SlackClient) listUsers(ctx context.Context) ([]user, error) {
	var all []user
	cursor := ""
	for {
		params := url.Values{}
		params.Set("limit", "999")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var out usersListResponse
		if err := c.apiGet(ctx, "users.list", params, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Members...)
		cursor = strings.TrimSpace(out.ResponseMetadata.NextCursor)
		if cursor == "" {
			break
		}
	}
	return all, nil
}

func (c *SlackClient) conversationHistory(ctx context.Context, channelID, oldest string, limit int) ([]message, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("limit", fmt.Sprintf("%d", limit))
	if oldest != "" && isSlackTS(oldest) {
		params.Set("oldest", oldest)
		params.Set("inclusive", "false")
	}
	var out conversationHistoryResponse
	if err := c.apiGet(ctx, "conversations.history", params, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *SlackClient) markConversationRead(ctx context.Context, channelID, ts string) error {
	form := url.Values{}
	form.Set("channel", channelID)
	form.Set("ts", ts)
	var out slackErrorResponse
	if err := c.apiForm(ctx, "conversations.mark", form, &out); err != nil {
		return err
	}
	return nil
}

func (c *SlackClient) openDM(ctx context.Context, userID string) (conversation, error) {
	form := url.Values{}
	form.Set("users", userID)
	var out conversationsOpenResponse
	if err := c.apiForm(ctx, "conversations.open", form, &out); err != nil {
		return conversation{}, err
	}
	return out.Channel, nil
}

func (c *SlackClient) postMessage(ctx context.Context, channelID, text, threadTS string) (*chatPostMessageResponse, error) {
	form := url.Values{}
	form.Set("channel", channelID)
	form.Set("text", text)
	if threadTS != "" {
		form.Set("thread_ts", threadTS)
	}
	var out chatPostMessageResponse
	if err := c.apiForm(ctx, "chat.postMessage", form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SlackClient) apiGet(ctx context.Context, method string, params url.Values, out interface{}) error {
	endpoint := slackAPIBase + method
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	return c.doJSON(req, method, out)
}

func (c *SlackClient) apiForm(ctx context.Context, method string, form url.Values, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackAPIBase+method, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.doJSON(req, method, out)
}

func (c *SlackClient) doJSON(req *http.Request, method string, out interface{}) error {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("%s failed: http %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}

	var apiErr slackErrorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil {
		if !apiErr.OK {
			return fmt.Errorf("%s failed: %s", method, emptyFallback(apiErr.Error, "unknown_error"))
		}
	}
	return nil
}

func collectUnreadConversations(ctx context.Context, client *SlackClient, convs []conversation) ([]unreadConversation, error) {
	var out []unreadConversation
	for _, conv := range convs {
		if !looksUnread(conv) {
			continue
		}
		msgs, err := client.conversationHistory(ctx, conv.ID, conv.LastRead, 12)
		if err != nil {
			return nil, fmt.Errorf("history for %s: %w", conversationTitle(conv, nil), err)
		}
		msgs = filterVisibleMessages(msgs)
		msgs = unreadTail(msgs, conv.LastRead)
		if len(msgs) == 0 {
			continue
		}
		sortMessagesAsc(msgs)
		out = append(out, unreadConversation{Conv: conv, Messages: msgs})
	}
	sort.Slice(out, func(i, j int) bool {
		return latestMessageTS(out[i].Messages) < latestMessageTS(out[j].Messages)
	})
	return out, nil
}

func looksUnread(conv conversation) bool {
	if conv.UnreadCountDisplay > 0 || conv.UnreadCount > 0 {
		return true
	}
	if conv.LastRead == "" && conv.Latest != nil && conv.Latest.TS != "" {
		return true
	}
	if conv.Latest != nil && isSlackTS(conv.LastRead) && isSlackTS(conv.Latest.TS) {
		return slackTSGreater(conv.Latest.TS, conv.LastRead)
	}
	return false
}

func filterVisibleMessages(in []message) []message {
	out := make([]message, 0, len(in))
	for _, msg := range in {
		if msg.Type != "message" {
			continue
		}
		if msg.Subtype == "message_deleted" || msg.Subtype == "channel_join" || msg.Subtype == "channel_leave" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func unreadTail(msgs []message, lastRead string) []message {
	if len(msgs) == 0 {
		return msgs
	}
	if lastRead == "" || !isSlackTS(lastRead) {
		if len(msgs) > 5 {
			return msgs[:5]
		}
		return msgs
	}
	var out []message
	for _, msg := range msgs {
		if isSlackTS(msg.TS) && slackTSGreater(msg.TS, lastRead) {
			out = append(out, msg)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func sortMessagesAsc(msgs []message) {
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].TS < msgs[j].TS
	})
}

func latestMessageTS(msgs []message) string {
	latest := ""
	for _, msg := range msgs {
		if latest == "" || msg.TS > latest {
			latest = msg.TS
		}
	}
	return latest
}

func buildUserNameMap(users []user) map[string]string {
	m := make(map[string]string, len(users))
	for _, u := range users {
		name := userDisplayName(u)
		if name != "" {
			m[u.ID] = name
		}
	}
	return m
}

func userDisplayName(u user) string {
	if s := strings.TrimSpace(u.Profile.DisplayName); s != "" {
		return s
	}
	if s := strings.TrimSpace(u.Profile.DisplayNameNormal); s != "" {
		return s
	}
	if s := strings.TrimSpace(u.RealName); s != "" {
		return s
	}
	if s := strings.TrimSpace(u.Profile.RealName); s != "" {
		return s
	}
	return strings.TrimSpace(u.Name)
}

func printUnreadConversation(item unreadConversation, userNames map[string]string, myUserID string) {
	fmt.Println(conversationHeading(item.Conv, userNames))
	for _, msg := range item.Messages {
		when := formatSlackTS(msg.TS)
		sender := senderLabel(msg, userNames, myUserID)
		text := strings.ReplaceAll(strings.TrimSpace(msg.Text), "\n", " ")
		if text == "" {
			text = "(no text)"
		}
		fmt.Printf("  %s %s: %s\n", when, sender, text)
	}
}

func conversationHeading(conv conversation, userNames map[string]string) string {
	title := conversationTitle(conv, userNames)
	switch {
	case conv.IsIM:
		return "[DM] " + title
	case conv.IsMPIM:
		return "[MPIM] " + title
	case conv.IsGroup:
		return "[private] " + title
	default:
		return "[#channel] " + title
	}
}

func conversationTitle(conv conversation, userNames map[string]string) string {
	switch {
	case conv.IsIM:
		if userNames != nil {
			if name := strings.TrimSpace(userNames[conv.User]); name != "" {
				return "@" + name
			}
		}
		if conv.User != "" {
			return "@" + conv.User
		}
	case conv.Name != "":
		if conv.IsChannel || conv.IsGroup {
			return "#" + conv.Name
		}
		return conv.Name
	}
	return conv.ID
}

func senderLabel(msg message, userNames map[string]string, myUserID string) string {
	if msg.User != "" {
		if msg.User == myUserID {
			return "me"
		}
		if name := strings.TrimSpace(userNames[msg.User]); name != "" {
			return name
		}
		return msg.User
	}
	if msg.Username != "" {
		return msg.Username
	}
	if msg.BotID != "" {
		return "bot"
	}
	return "unknown"
}

func formatSlackTS(ts string) string {
	secPart := ts
	if i := strings.IndexByte(ts, '.'); i >= 0 {
		secPart = ts[:i]
	}
	secs, err := parseInt64(secPart)
	if err != nil {
		return ts
	}
	return time.Unix(secs, 0).Local().Format("2006-01-02 15:04")
}

func parseInt64(s string) (int64, error) {
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("not an integer: %q", s)
		}
		n = n*10 + int64(ch-'0')
	}
	return n, nil
}

func resolveTarget(ctx context.Context, client *SlackClient, target string) (conversationID, display string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", errors.New("empty target")
	}

	if strings.HasPrefix(target, "#") {
		name := strings.TrimPrefix(target, "#")
		convs, err := client.listAllConversations(ctx)
		if err != nil {
			return "", "", fmt.Errorf("resolve channel name: %w", err)
		}
		for _, conv := range convs {
			if conv.Name == name {
				return conv.ID, "#" + name, nil
			}
		}
		return "", "", fmt.Errorf("channel not found: #%s", name)
	}

	if strings.HasPrefix(target, "@") {
		username := strings.TrimPrefix(target, "@")
		users, err := client.listUsers(ctx)
		if err != nil {
			return "", "", fmt.Errorf("resolve user: %w", err)
		}
		matched, err := findUserByName(users, username)
		if err != nil {
			return "", "", err
		}
		dm, err := client.openDM(ctx, matched.ID)
		if err != nil {
			return "", "", fmt.Errorf("open dm with @%s: %w", username, err)
		}
		return dm.ID, "@" + userDisplayName(matched), nil
	}

	if isConversationID(target) {
		return target, target, nil
	}

	if isUserID(target) {
		dm, err := client.openDM(ctx, target)
		if err != nil {
			return "", "", fmt.Errorf("open dm with %s: %w", target, err)
		}
		return dm.ID, target, nil
	}

	return "", "", fmt.Errorf("unsupported target: %s", target)
}

func findUserByName(users []user, name string) (user, error) {
	name = strings.TrimSpace(name)
	var matches []user
	for _, u := range users {
		if u.Deleted || u.IsBot {
			continue
		}
		candidates := []string{
			u.Name,
			u.Profile.DisplayName,
			u.Profile.DisplayNameNormal,
			u.RealName,
		}
		for _, c := range candidates {
			if strings.EqualFold(strings.TrimSpace(c), name) {
				matches = append(matches, u)
				break
			}
		}
	}
	if len(matches) == 0 {
		return user{}, fmt.Errorf("user not found: @%s", name)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, u := range matches {
			ids = append(ids, fmt.Sprintf("%s(%s)", userDisplayName(u), u.ID))
		}
		return user{}, fmt.Errorf("multiple users matched @%s: %s", name, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func splitThreadTarget(target string) (string, string) {
	i := strings.LastIndex(target, ":")
	if i <= 0 || i == len(target)-1 {
		return target, ""
	}
	left := target[:i]
	right := target[i+1:]
	if !isSlackTS(right) {
		return target, ""
	}
	return left, right
}

func isConversationID(s string) bool {
	if len(s) < 2 {
		return false
	}
	switch s[0] {
	case 'C', 'G', 'D':
		for _, ch := range s[1:] {
			if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isUserID(s string) bool {
	if len(s) < 2 || s[0] != 'U' {
		return false
	}
	for _, ch := range s[1:] {
		if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}

func isSlackTS(s string) bool {
	if s == "" {
		return false
	}
	dot := 0
	for i, ch := range s {
		if ch == '.' {
			dot++
			if i == 0 || i == len(s)-1 {
				return false
			}
			continue
		}
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return dot == 1
}

func slackTSGreater(a, b string) bool {
	return a > b
}
