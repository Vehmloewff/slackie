package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const unreadPollInterval = 15 * time.Second

type unreadConversation struct {
	Conv     conversation
	Messages []message
}

func runRead(wait bool) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.LastSeen == nil {
		cfg.LastSeen = map[string]string{}
	}

	client := &SlackClient{HTTPClient: &http.Client{Timeout: 30 * time.Second}, Token: readAccessToken(cfg)}
	ctx := context.Background()

	users, err := client.listUsers(ctx)
	if err != nil {
		return fmt.Errorf("load users: %w", err)
	}
	userNames := buildUserNameMap(users)

	convs, err := client.listConversations(ctx, configConversationTypes(cfg))
	if err != nil {
		return fmt.Errorf("load conversations: %w", err)
	}

	unreads, err := findUnreadConversations(ctx, client, convs, &cfg)
	if err != nil {
		return err
	}
	myUserID := mentionUserID(cfg)
	if len(unreads) > 0 {
		if err := printAndRememberUnread(ctx, client, unreads, userNames, myUserID, &cfg); err != nil {
			return err
		}
		_, err := saveConfig(cfg)
		return err
	}
	if !wait {
		_, err := saveConfig(cfg)
		return err
	}

	if cfg.readAppMentionsOnly() {
		fmt.Println("No unread mentions. Waiting for the next mention...")
	} else {
		fmt.Println("No unread messages. Waiting for the next message...")
	}
	appToken := socketModeAppToken(cfg)
	if appToken == "" {
		return fmt.Errorf("slackie read --wait requires a Slack app-level token; run slackie setup and paste an xapp-... token")
	}
	if err := waitForSocketModeMessage(ctx, client, appToken, userNames, myUserID, &cfg); err != nil {
		return err
	}
	_, err = saveConfig(cfg)
	return err
}

func configConversationTypes(cfg Config) string {
	types := []string{"public_channel"}
	if cfg.readPrivateChannels() {
		types = append(types, "private_channel")
	}
	if cfg.readDMs() {
		types = append(types, "im")
	}
	if cfg.readMessageGroups() {
		types = append(types, "mpim")
	}
	return strings.Join(types, ",")
}

func findUnreadConversations(ctx context.Context, client *SlackClient, convs []conversation, cfg *Config) ([]unreadConversation, error) {
	var out []unreadConversation

	for _, conv := range convs {
		baseline := bestBaseline(conv, cfg.LastSeen[conv.ID])
		if baseline == "" {
			// First run for this token: avoid dumping history and avoid one
			// conversations.history request per channel. Use Slack's latest marker as
			// our high-water mark; newly arriving messages will be fetched later.
			rememberConversationLatest(cfg, conv)
			continue
		}
		if !conversationMayHaveNewMessages(conv, baseline) {
			continue
		}

		msgs, err := fetchConversationHistory(ctx, client, conv.ID, HistoryOptions{Oldest: baseline, Limit: 50})
		if err != nil {
			return nil, fmt.Errorf("history for %s: %w", conversationTitle(conv, nil), err)
		}

		visible := relevantUnreadMessages(conv, msgs, mentionUserID(*cfg), cfg.readAppMentionsOnly())
		if len(visible) == 0 {
			rememberLatest(cfg, conv.ID, msgs)
			continue
		}

		sortMessagesAsc(visible)
		out = append(out, unreadConversation{Conv: conv, Messages: visible})
	}

	sort.Slice(out, func(i, j int) bool {
		return latestMessageTS(out[i].Messages) < latestMessageTS(out[j].Messages)
	})
	return out, nil
}

func bestBaseline(conv conversation, lastSeen string) string {
	if isSlackTS(lastSeen) {
		return lastSeen
	}
	if isSlackTS(conv.LastRead) {
		return conv.LastRead
	}
	return ""
}

func hydrateConversationReadState(ctx context.Context, client *SlackClient, conv conversation) (conversation, error) {
	if conv.LastRead != "" && conv.Latest != nil && conv.Latest.TS != "" {
		return conv, nil
	}

	info, err := client.getConversationInfo(ctx, conv.ID)
	if err != nil {
		return conv, err
	}
	if conv.LastRead == "" {
		conv.LastRead = info.LastRead
	}
	if conv.Latest == nil || conv.Latest.TS == "" {
		conv.Latest = info.Latest
	}
	if conv.UnreadCount == 0 {
		conv.UnreadCount = info.UnreadCount
	}
	if conv.UnreadCountDisplay == 0 {
		conv.UnreadCountDisplay = info.UnreadCountDisplay
	}
	if conv.User == "" {
		conv.User = info.User
	}
	if conv.Name == "" {
		conv.Name = info.Name
	}
	return conv, nil
}

func waitForNextMessage(ctx context.Context, client *SlackClient, convs []conversation, userNames map[string]string, myUserID string, cfg *Config) error {
	for _, conv := range convs {
		if cfg.LastSeen[conv.ID] == "" {
			rememberConversationLatest(cfg, conv)
		}
	}

	for {
		time.Sleep(unreadPollInterval)

		freshConvs, err := client.listConversations(ctx, configConversationTypes(*cfg))
		if err != nil {
			if isFatalSlackError(err) {
				return err
			}
			fmt.Fprintf(os.Stderr, "warning: poll conversations failed: %v\n", err)
			continue
		}

		for _, conv := range freshConvs {
			baseline := cfg.LastSeen[conv.ID]
			if baseline == "" {
				rememberConversationLatest(cfg, conv)
				continue
			}
			if !conversationMayHaveNewMessages(conv, baseline) {
				continue
			}

			msgs, err := fetchConversationHistory(ctx, client, conv.ID, HistoryOptions{Oldest: baseline, Limit: 12})
			if err != nil {
				if isFatalSlackError(err) {
					return err
				}
				fmt.Fprintf(os.Stderr, "warning: poll %s failed: %v\n", conversationTitle(conv, nil), err)
				continue
			}
			if len(msgs) == 0 {
				rememberConversationLatest(cfg, conv)
				continue
			}

			visible := relevantUnreadMessages(conv, msgs, myUserID, cfg.readAppMentionsOnly())
			rememberLatest(cfg, conv.ID, msgs)
			if len(visible) == 0 {
				continue
			}

			sortMessagesAsc(visible)
			item := unreadConversation{Conv: conv, Messages: visible}
			if err := printUnreadConversation(ctx, client, item, userNames, myUserID); err != nil {
				return fmt.Errorf("print %s: %w", conversationTitle(conv, userNames), err)
			}
			return nil
		}
	}
}

func printAndRememberUnread(ctx context.Context, client *SlackClient, unreads []unreadConversation, userNames map[string]string, myUserID string, cfg *Config) error {
	for i, item := range unreads {
		if err := printUnreadConversation(ctx, client, item, userNames, myUserID); err != nil {
			return fmt.Errorf("print %s: %w", conversationTitle(item.Conv, userNames), err)
		}
		rememberLatest(cfg, item.Conv.ID, item.Messages)
		if i != len(unreads)-1 {
			fmt.Println()
		}
	}
	return nil
}

func rememberLatest(cfg *Config, channelID string, msgs []message) {
	if cfg.LastSeen == nil {
		cfg.LastSeen = map[string]string{}
	}
	latest := latestMessageTS(msgs)
	if latest != "" && slackTSGreater(latest, cfg.LastSeen[channelID]) {
		cfg.LastSeen[channelID] = latest
	}
}

func rememberConversationLatest(cfg *Config, conv conversation) {
	if cfg.LastSeen == nil {
		cfg.LastSeen = map[string]string{}
	}
	if conv.Latest != nil && isSlackTS(conv.Latest.TS) && slackTSGreater(conv.Latest.TS, cfg.LastSeen[conv.ID]) {
		cfg.LastSeen[conv.ID] = conv.Latest.TS
	}
}

func conversationMayHaveNewMessages(conv conversation, baseline string) bool {
	if conv.Latest != nil && isSlackTS(conv.Latest.TS) {
		return slackTSGreater(conv.Latest.TS, baseline)
	}
	return conv.UnreadCount > 0 || conv.UnreadCountDisplay > 0
}

func relevantUnreadMessages(conv conversation, in []message, myUserID string, readAppMentionsOnly bool) []message {
	out := make([]message, 0, len(in))
	for _, msg := range filterVisibleMessages(in) {
		if msg.User == myUserID || (myUserID != "" && strings.Contains(msg.Text, "<@"+myUserID+">") && msg.User == myUserID) {
			continue
		}
		if conv.IsIM || conv.IsMPIM {
			out = append(out, msg)
			continue
		}
		if !readAppMentionsOnly || (myUserID != "" && strings.Contains(msg.Text, "<@"+myUserID+">")) {
			out = append(out, msg)
		}
	}
	return out
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

func printUnreadConversation(ctx context.Context, client *SlackClient, item unreadConversation, userNames map[string]string, myUserID string) error {
	fmt.Println(conversationHeading(item.Conv, userNames))
	for _, msg := range item.Messages {
		when := formatSlackTS(msg.TS)
		sender := senderLabel(msg, userNames, myUserID)
		text := strings.ReplaceAll(strings.TrimSpace(msg.Text), "\n", " ")
		if text == "" {
			text = "(no text)"
		}
		fmt.Printf("  %s %s: %s\n", when, sender, text)
		if target := messageThreadingTarget(item.Conv, msg, userNames); target != "" {
			fmt.Printf("    threading target: %s\n", target)
		}

		attachmentPaths, err := downloadMessageAttachments(ctx, client, msg)
		if err != nil {
			return err
		}
		for _, path := range attachmentPaths {
			fmt.Printf("    %s\n", path)
		}
	}
	return nil
}

func messageThreadingTarget(conv conversation, msg message, userNames map[string]string) string {
	return threadingTarget(conversationTitle(conv, userNames), msg.TS, msg.ThreadTS)
}

func downloadMessageAttachments(ctx context.Context, client *SlackClient, msg message) ([]string, error) {
	var out []string
	seenURLs := map[string]struct{}{}

	download := func(rawURL, suggestedName string) error {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return nil
		}
		if _, ok := seenURLs[rawURL]; ok {
			return nil
		}
		seenURLs[rawURL] = struct{}{}

		path, err := client.downloadToTempFile(ctx, rawURL, suggestedName)
		if err != nil {
			if strings.Contains(err.Error(), errAttachmentIsHTML.Error()) {
				return nil
			}
			return err
		}
		out = append(out, path)
		return nil
	}

	for _, file := range msg.Files {
		if file.IsExternal {
			continue
		}
		suggestedName := strings.TrimSpace(file.Name)
		if suggestedName == "" {
			suggestedName = strings.TrimSpace(file.Title)
		}
		preferredURL := strings.TrimSpace(file.URLPrivateDownload)
		fallbackURL := strings.TrimSpace(file.URLPrivate)
		if preferredURL == "" {
			preferredURL, fallbackURL = fallbackURL, ""
		}
		if err := download(preferredURL, suggestedName); err != nil {
			if fallbackURL == "" {
				return nil, err
			}
			if err := download(fallbackURL, suggestedName); err != nil {
				return nil, err
			}
		}
	}

	for _, attachment := range msg.Attachments {
		suggestedName := strings.TrimSpace(attachment.Title)
		for _, rawURL := range []string{attachment.ImageURL, attachment.ThumbURL} {
			if err := download(rawURL, suggestedName); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
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

func isFatalSlackError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	fatalMarkers := []string{
		"invalid_auth",
		"account_inactive",
		"token_revoked",
		"not_authed",
		"http 401",
	}
	for _, marker := range fatalMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
