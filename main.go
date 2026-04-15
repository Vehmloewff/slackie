package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	slackClientID    = "10137959977282.10936753440897"
	slackRedirectURI = "slackie://callback"
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
	"channels:write",
	"groups:write",
	"im:write",
	"mpim:write",
	"users:read",
	"files:read",
	"files:write",
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
	case "unauth":
		if len(args) != 1 {
			return errors.New("usage: slackie unauth")
		}
		return cmdUnauth()
	case "read":
		wait, err := parseReadArgs(args[1:])
		if err != nil {
			return err
		}
		return runRead(wait)
	case "send":
		target, attachments, err := parseSendArgs(args[1:])
		if err != nil {
			return err
		}
		return cmdSend(target, attachments)
	default:
		printHelp()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func parseReadArgs(args []string) (bool, error) {
	wait := false
	for _, arg := range args {
		switch arg {
		case "--wait":
			wait = true
		default:
			return false, fmt.Errorf("usage: slackie read [--wait]")
		}
	}
	return wait, nil
}

func parseSendArgs(args []string) (string, []string, error) {
	var target string
	var attachments []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--attach":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return "", nil, errors.New("usage: slackie send [--attach PATH ...] <target>")
			}
			attachments = append(attachments, args[i])
		case strings.HasPrefix(arg, "--attach="):
			path := strings.TrimSpace(strings.TrimPrefix(arg, "--attach="))
			if path == "" {
				return "", nil, errors.New("usage: slackie send [--attach PATH ...] <target>")
			}
			attachments = append(attachments, path)
		default:
			if target != "" {
				return "", nil, errors.New("usage: slackie send [--attach PATH ...] <target>")
			}
			target = arg
		}
	}

	if strings.TrimSpace(target) == "" {
		return "", nil, errors.New("usage: slackie send [--attach PATH ...] <target>")
	}
	return target, attachments, nil
}

func printHelp() {
	fmt.Println("slackie - tiny Slack CLI")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  slackie auth")
	fmt.Println("  slackie unauth")
	fmt.Println("  slackie read")
	fmt.Println("  slackie read --wait")
	fmt.Println("  slackie send [--attach PATH ...] <target>")
	fmt.Println("  slackie help")
	fmt.Println()
	fmt.Println("read:")
	fmt.Println("  slackie read")
	fmt.Println("    List unread conversations, mark them read, and exit.")
	fmt.Println("  slackie read --wait")
	fmt.Println("    Run the normal unread scan first. If none exist, wait for the next")
	fmt.Println("    newly arrived message, print it, mark it read, and exit.")
	fmt.Println()
	fmt.Println("send:")
	fmt.Println("  slackie send [--attach PATH ...] <target>")
	fmt.Println("    Read the message body from stdin and send it to the target.")
	fmt.Println("    Use --attach multiple times to upload local files.")
	fmt.Println()
	fmt.Println("Targets:")
	fmt.Println("  #channel-name")
	fmt.Println("  C12345678")
	fmt.Println("  @alice")
	fmt.Println("  #channel-name:1740000000.123456")
	fmt.Println("  C12345678:1740000000.123456")
}
