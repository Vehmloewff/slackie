package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const slackAPIBase = "https://slack.com/api/"

type SlackClient struct {
	HTTPClient *http.Client
	Token      string
}

type slackErrorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
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

type conversationInfoResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error"`
	Channel conversation `json:"channel"`
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

type HistoryOptions struct {
	Oldest string
	Limit  int
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

func (c *SlackClient) getConversationInfo(ctx context.Context, channelID string) (conversation, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	var out conversationInfoResponse
	if err := c.apiGet(ctx, "conversations.info", params, &out); err != nil {
		return conversation{}, err
	}
	return out.Channel, nil
}

func fetchConversationHistory(ctx context.Context, client *SlackClient, channelID string, opts HistoryOptions) ([]message, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 12
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("limit", fmt.Sprintf("%d", limit))
	if opts.Oldest != "" && isSlackTS(opts.Oldest) {
		params.Set("oldest", opts.Oldest)
		params.Set("inclusive", "false")
	}

	var out conversationHistoryResponse
	if err := client.apiGet(ctx, "conversations.history", params, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func markConversationRead(ctx context.Context, client *SlackClient, channelID, ts string) error {
	form := url.Values{}
	form.Set("channel", channelID)
	form.Set("ts", ts)
	var out slackErrorResponse
	if err := client.apiForm(ctx, "conversations.mark", form, &out); err != nil {
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
	if err := json.Unmarshal(body, &apiErr); err == nil && !apiErr.OK {
		return fmt.Errorf("%s failed: %s", method, emptyFallback(apiErr.Error, "unknown_error"))
	}
	return nil
}
