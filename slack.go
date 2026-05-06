package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const slackAPIBase = "https://slack.com/api/"

var errAttachmentIsHTML = errors.New("attachment response was html")

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
	Type        string            `json:"type"`
	User        string            `json:"user"`
	Text        string            `json:"text"`
	TS          string            `json:"ts"`
	ThreadTS    string            `json:"thread_ts"`
	Subtype     string            `json:"subtype"`
	BotID       string            `json:"bot_id"`
	Username    string            `json:"username"`
	Files       []slackFile       `json:"files"`
	Attachments []slackAttachment `json:"attachments"`
}

type slackFile struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Title              string `json:"title"`
	Mimetype           string `json:"mimetype"`
	Filetype           string `json:"filetype"`
	Mode               string `json:"mode"`
	IsExternal         bool   `json:"is_external"`
	URLPrivate         string `json:"url_private"`
	URLPrivateDownload string `json:"url_private_download"`
}

type slackAttachment struct {
	Title     string `json:"title"`
	TitleLink string `json:"title_link"`
	FromURL   string `json:"from_url"`
	ImageURL  string `json:"image_url"`
	ThumbURL  string `json:"thumb_url"`
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

type fileUploadResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	File  struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"file"`
}

type getUploadURLExternalResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
}

type completeUploadExternalResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Files []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"files"`
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

func (c *SlackClient) uploadFile(ctx context.Context, channelID, threadTS, initialComment, path string) (*fileUploadResponse, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open attachment %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat attachment %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("attachment is a directory: %s", path)
	}

	filename := filepath.Base(path)
	uploadURL, fileID, err := c.getUploadURLExternal(ctx, filename, info.Size())
	if err != nil {
		return nil, err
	}
	if err := c.putExternalFile(ctx, uploadURL, f, info.Size()); err != nil {
		return nil, err
	}
	return c.completeUploadExternal(ctx, channelID, threadTS, initialComment, fileID, filename)
}

func (c *SlackClient) getUploadURLExternal(ctx context.Context, filename string, length int64) (string, string, error) {
	form := url.Values{}
	form.Set("filename", filename)
	form.Set("length", strconv.FormatInt(length, 10))

	var out getUploadURLExternalResponse
	if err := c.apiForm(ctx, "files.getUploadURLExternal", form, &out); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(out.UploadURL), strings.TrimSpace(out.FileID), nil
}

func (c *SlackClient) putExternalFile(ctx context.Context, uploadURL string, r io.Reader, length int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, r)
	if err != nil {
		return fmt.Errorf("build external upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = length

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("external upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("external upload failed: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *SlackClient) completeUploadExternal(ctx context.Context, channelID, threadTS, initialComment, fileID, filename string) (*fileUploadResponse, error) {
	filesJSON, err := json.Marshal([]map[string]string{{
		"id":    fileID,
		"title": filename,
	}})
	if err != nil {
		return nil, fmt.Errorf("encode upload completion payload: %w", err)
	}

	form := url.Values{}
	form.Set("files", string(filesJSON))
	form.Set("channel_id", channelID)
	if threadTS != "" {
		form.Set("thread_ts", threadTS)
	}
	if strings.TrimSpace(initialComment) != "" {
		form.Set("initial_comment", initialComment)
	}

	var out completeUploadExternalResponse
	if err := c.apiForm(ctx, "files.completeUploadExternal", form, &out); err != nil {
		return nil, err
	}

	resp := &fileUploadResponse{OK: out.OK, Error: out.Error}
	if len(out.Files) > 0 {
		resp.File.ID = out.Files[0].ID
		resp.File.Name = out.Files[0].Name
	}
	return resp, nil
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

func (c *SlackClient) downloadToTempFile(ctx context.Context, rawURL, suggestedName string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("missing download url")
	}

	resp, finalURL, err := c.getWithAuthRedirects(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("download attachment failed: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type"))); strings.HasPrefix(contentType, "text/html") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("%w: %s", errAttachmentIsHTML, strings.TrimSpace(string(body)))
	}

	suffix := attachmentTempSuffix(finalURL, suggestedName)
	file, err := os.CreateTemp("", "slackie-attachment-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("create temp attachment file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write temp attachment file: %w", err)
	}
	return file.Name(), nil
}

func (c *SlackClient) getWithAuthRedirects(ctx context.Context, rawURL string) (*http.Response, string, error) {
	client := *c.HTTPClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	currentURL := rawURL
	for redirectCount := 0; redirectCount < 10; redirectCount++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("build download request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)

		resp, err := client.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("download attachment failed: %w", err)
		}

		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return resp, currentURL, nil
		}

		location := strings.TrimSpace(resp.Header.Get("Location"))
		resp.Body.Close()
		if location == "" {
			return nil, "", fmt.Errorf("download attachment failed: redirect missing location")
		}

		nextURL, err := resolveRedirectURL(currentURL, location)
		if err != nil {
			return nil, "", fmt.Errorf("resolve attachment redirect: %w", err)
		}
		currentURL = nextURL
	}

	return nil, "", fmt.Errorf("download attachment failed: too many redirects")
}

func resolveRedirectURL(baseURL, location string) (string, error) {
	loc, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	if loc.IsAbs() {
		return loc.String(), nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(loc).String(), nil
}

func attachmentTempSuffix(rawURL, suggestedName string) string {
	for _, candidate := range []string{strings.TrimSpace(suggestedName), strings.TrimSpace(rawURL)} {
		if candidate == "" {
			continue
		}
		if u, err := url.Parse(candidate); err == nil && u.Path != "" {
			candidate = u.Path
		}
		ext := strings.ToLower(filepath.Ext(candidate))
		if ext != "" && len(ext) <= 10 {
			return ext
		}
	}
	return ".img"
}

func (c *SlackClient) doJSON(req *http.Request, method string, out interface{}) error {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("%s failed: http %d: retry after %s: %s", method, resp.StatusCode, retryAfter, strings.TrimSpace(string(body)))
	}
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
