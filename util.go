package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func emptyFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
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
		if conv, err := client.getConversationInfo(ctx, target); err == nil {
			return target, conversationTitle(conv, nil), nil
		}
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
		candidates := []string{u.Name, u.Profile.DisplayName, u.Profile.DisplayNameNormal, u.RealName}
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

func cmdSend(target string, attachmentPaths []string, mrkdwn bool) error {
	cfg, _, err := loadConfig()
	if err != nil {
		return err
	}
	client := &SlackClient{HTTPClient: &http.Client{Timeout: 30 * time.Second}, Token: writeAccessToken(cfg)}
	ctx := context.Background()

	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	text := strings.TrimRight(string(body), "\r\n")
	if strings.TrimSpace(text) == "" && len(attachmentPaths) == 0 {
		return errors.New("nothing to send: provide stdin message text and/or --attach PATH")
	}
	text, err = resolveUserMentionsInText(ctx, client, text)
	if err != nil {
		return err
	}

	convTarget, threadTS := splitThreadTarget(target)
	convID, display, err := resolveTarget(ctx, client, convTarget)
	if err != nil {
		return err
	}

	if len(attachmentPaths) == 0 {
		resp, err := client.postMessage(ctx, convID, text, threadTS, mrkdwn)
		if err != nil {
			return err
		}
		threadingTarget := sentThreadingTarget(display, resp.TS, threadTS)
		if threadTS != "" {
			fmt.Printf("sent to %s thread %s (threading target: %s)\n", display, threadTS, threadingTarget)
		} else {
			fmt.Printf("sent to %s (threading target: %s)\n", display, threadingTarget)
		}
		return nil
	}

	remainingComment := text
	for i, path := range attachmentPaths {
		comment := ""
		if i == 0 {
			comment = remainingComment
		}
		resp, err := client.uploadFile(ctx, convID, threadTS, comment, path)
		if err != nil {
			return err
		}
		threadingTarget := sentThreadingTarget(display, resp.File.TS, threadTS)
		if threadTS != "" {
			if threadingTarget != "" {
				fmt.Printf("uploaded %s to %s thread %s (threading target: %s)\n", path, display, threadTS, threadingTarget)
			} else {
				fmt.Printf("uploaded %s to %s thread %s (file id: %s)\n", path, display, threadTS, resp.File.ID)
			}
		} else {
			if threadingTarget != "" {
				fmt.Printf("uploaded %s to %s (threading target: %s)\n", path, display, threadingTarget)
			} else {
				fmt.Printf("uploaded %s to %s (file id: %s)\n", path, display, resp.File.ID)
			}
		}
	}
	return nil
}

func sentThreadingTarget(display, messageTS, threadTS string) string {
	return threadingTarget(display, messageTS, threadTS)
}

func threadingTarget(display, messageTS, threadTS string) string {
	display = strings.TrimSpace(display)
	if display == "" {
		return ""
	}
	if isSlackTS(threadTS) {
		return display + ":" + threadTS
	}
	if isSlackTS(messageTS) {
		return display + ":" + messageTS
	}
	return ""
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
	if len(s) < 2 || (s[0] != 'U' && s[0] != 'W') {
		return false
	}
	for _, ch := range s[1:] {
		if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}

func readableUserMentions(text string, userNames map[string]string) string {
	if len(userNames) == 0 {
		return text
	}
	return replaceUserMentions(text, func(raw string) (string, bool) {
		id, ok := parseMentionUserID(raw)
		if !ok {
			return "", false
		}
		name := strings.TrimSpace(userNames[id])
		if name == "" {
			return "", false
		}
		return "<@" + name + ">", true
	})
}

func resolveUserMentionsInText(ctx context.Context, client *SlackClient, text string) (string, error) {
	if len(userMentionNames(text)) == 0 {
		return text, nil
	}
	users, err := client.listUsers(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve user mentions: %w", err)
	}
	return resolveUserMentionNames(text, users)
}

func resolveUserMentionNames(text string, users []user) (string, error) {
	resolved := map[string]string{}
	for _, name := range userMentionNames(text) {
		u, err := findMentionUserByName(users, name)
		if err != nil {
			return "", err
		}
		resolved[strings.ToLower(name)] = u.ID
	}

	return replaceUserMentions(text, func(raw string) (string, bool) {
		if _, ok := parseMentionUserID(raw); ok {
			return "", false
		}
		name := strings.TrimSpace(raw)
		if strings.HasPrefix(name, "@") {
			name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
		}
		id := resolved[strings.ToLower(name)]
		if id == "" {
			return "", false
		}
		return "<@" + id + ">", true
	}), nil
}

func userMentionNames(text string) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, raw := range userMentionContents(text) {
		if _, ok := parseMentionUserID(raw); ok {
			continue
		}
		name := strings.TrimSpace(raw)
		if strings.HasPrefix(name, "@") {
			name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
		}
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

func userMentionContents(text string) []string {
	var out []string
	for i := 0; i < len(text); {
		start := strings.Index(text[i:], "<@")
		if start < 0 {
			break
		}
		start += i
		end := strings.IndexByte(text[start+2:], '>')
		if end < 0 {
			break
		}
		end += start + 2
		out = append(out, text[start+2:end])
		i = end + 1
	}
	return out
}

func replaceUserMentions(text string, replacement func(raw string) (string, bool)) string {
	var b strings.Builder
	changed := false
	for i := 0; i < len(text); {
		start := strings.Index(text[i:], "<@")
		if start < 0 {
			b.WriteString(text[i:])
			break
		}
		start += i
		end := strings.IndexByte(text[start+2:], '>')
		if end < 0 {
			b.WriteString(text[i:])
			break
		}
		end += start + 2
		b.WriteString(text[i:start])
		if repl, ok := replacement(text[start+2 : end]); ok {
			b.WriteString(repl)
			changed = true
		} else {
			b.WriteString(text[start : end+1])
		}
		i = end + 1
	}
	if !changed {
		return text
	}
	return b.String()
}

func parseMentionUserID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if pipe := strings.IndexByte(id, '|'); pipe >= 0 {
		id = strings.TrimSpace(id[:pipe])
	}
	if !isUserID(id) {
		return "", false
	}
	return id, true
}

func findMentionUserByName(users []user, name string) (user, error) {
	name = strings.TrimSpace(name)
	var matches []user
	for _, u := range users {
		if u.Deleted {
			continue
		}
		candidates := []string{u.Name, u.Profile.DisplayName, u.Profile.DisplayNameNormal, u.RealName, u.Profile.RealName}
		for _, c := range candidates {
			if strings.EqualFold(strings.TrimSpace(c), name) {
				matches = append(matches, u)
				break
			}
		}
	}
	if len(matches) == 0 {
		return user{}, fmt.Errorf("User not found: %s", name)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, u := range matches {
			ids = append(ids, fmt.Sprintf("%s(%s)", userDisplayName(u), u.ID))
		}
		return user{}, fmt.Errorf("multiple users matched <@%s>: %s", name, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func isSlackTS(s string) bool {
	if s == "" {
		return false
	}
	dot := 0
	nonZero := false
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
		if ch != '0' {
			nonZero = true
		}
	}
	return dot == 1 && nonZero
}

func slackTimestamp(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

func slackTSGreater(a, b string) bool {
	return a > b
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Minute
	}
	secs, err := parseInt64(raw)
	if err != nil || secs <= 0 {
		return time.Minute
	}
	return time.Duration(secs) * time.Second
}
