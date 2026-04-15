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

const unreadPollInterval = 3 * time.Second

type unreadConversation struct {
	Conv     conversation
	Messages []message
}

func runRead(wait bool) error {
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

	unreads, recentByConversation, err := findUnreadConversations(ctx, client, convs)
	if err != nil {
		return err
	}
	if len(unreads) > 0 {
		return printAndMarkUnread(ctx, client, unreads, userNames, cfg.UserID)
	}
	if !wait {
		return nil
	}

	baselines, err := buildBaselines(ctx, client, convs, recentByConversation)
	if err != nil {
		return err
	}

	fmt.Println("No unread messages. Waiting for the next message...")
	return waitForNextMessage(ctx, client, convs, baselines, userNames, cfg.UserID)
}

func findUnreadConversations(ctx context.Context, client *SlackClient, convs []conversation) ([]unreadConversation, map[string][]message, error) {
	var out []unreadConversation
	recentByConversation := make(map[string][]message, len(convs))

	for _, conv := range convs {
		conv, err := hydrateConversationReadState(ctx, client, conv)
		if err != nil {
			return nil, nil, fmt.Errorf("load conversation info for %s: %w", conversationTitle(conv, nil), err)
		}

		msgs, err := fetchConversationHistory(ctx, client, conv.ID, HistoryOptions{Limit: 12})
		if err != nil {
			return nil, nil, fmt.Errorf("history for %s: %w", conversationTitle(conv, nil), err)
		}
		recentByConversation[conv.ID] = msgs

		visible := filterVisibleMessages(msgs)
		if len(visible) == 0 {
			continue
		}

		if isSlackTS(conv.LastRead) {
			visible = unreadTail(visible, conv.LastRead)
		} else if looksUnread(conv) {
			if len(visible) > 5 {
				visible = visible[:5]
			}
		} else {
			continue
		}
		if len(visible) == 0 {
			continue
		}

		sortMessagesAsc(visible)
		out = append(out, unreadConversation{Conv: conv, Messages: visible})
	}

	sort.Slice(out, func(i, j int) bool {
		return latestMessageTS(out[i].Messages) < latestMessageTS(out[j].Messages)
	})
	return out, recentByConversation, nil
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

func buildBaselines(ctx context.Context, client *SlackClient, convs []conversation, recentByConversation map[string][]message) (map[string]string, error) {
	baselines := make(map[string]string, len(convs))
	for _, conv := range convs {
		if msgs, ok := recentByConversation[conv.ID]; ok {
			baselines[conv.ID] = latestMessageTS(msgs)
			continue
		}

		msgs, err := fetchConversationHistory(ctx, client, conv.ID, HistoryOptions{Limit: 1})
		if err != nil {
			return nil, fmt.Errorf("build baseline for %s: %w", conversationTitle(conv, nil), err)
		}
		baselines[conv.ID] = latestMessageTS(msgs)
	}
	return baselines, nil
}

func waitForNextMessage(ctx context.Context, client *SlackClient, convs []conversation, baselines map[string]string, userNames map[string]string, myUserID string) error {
	for {
		time.Sleep(unreadPollInterval)

		for _, conv := range convs {
			msgs, err := fetchConversationHistory(ctx, client, conv.ID, HistoryOptions{
				Oldest: baselines[conv.ID],
				Limit:  12,
			})
			if err != nil {
				if isFatalSlackError(err) {
					return err
				}
				fmt.Fprintf(os.Stderr, "warning: poll %s failed: %v\n", conversationTitle(conv, nil), err)
				continue
			}
			if len(msgs) == 0 {
				continue
			}

			baselines[conv.ID] = latestMessageTS(msgs)
			visible := filterVisibleMessages(msgs)
			if len(visible) == 0 {
				continue
			}

			sortMessagesAsc(visible)
			item := unreadConversation{Conv: conv, Messages: visible}
			if err := printUnreadConversation(ctx, client, item, userNames, myUserID); err != nil {
				return fmt.Errorf("print %s: %w", conversationTitle(conv, userNames), err)
			}

			latestTS := latestMessageTS(visible)
			if latestTS != "" {
				if err := markConversationRead(ctx, client, conv.ID, latestTS); err != nil {
					return fmt.Errorf("mark read for %s: %w", conversationTitle(conv, userNames), err)
				}
			}
			return nil
		}
	}
}

func printAndMarkUnread(ctx context.Context, client *SlackClient, unreads []unreadConversation, userNames map[string]string, myUserID string) error {
	for i, item := range unreads {
		if err := printUnreadConversation(ctx, client, item, userNames, myUserID); err != nil {
			return fmt.Errorf("print %s: %w", conversationTitle(item.Conv, userNames), err)
		}
		latestTS := latestMessageTS(item.Messages)
		if latestTS != "" {
			if err := markConversationRead(ctx, client, item.Conv.ID, latestTS); err != nil {
				return fmt.Errorf("mark read for %s: %w", conversationTitle(item.Conv, userNames), err)
			}
		}
		if i != len(unreads)-1 {
			fmt.Println()
		}
	}
	return nil
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
