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

func printHelp() {
	fmt.Println("slackie - tiny Slack CLI")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  slackie auth")
	fmt.Println("  slackie unauth")
	fmt.Println("  slackie read")
	fmt.Println("  slackie read --wait")
	fmt.Println("  slackie send <target> <message>")
	fmt.Println("  slackie help")
	fmt.Println()
	fmt.Println("read:")
	fmt.Println("  slackie read")
	fmt.Println("    List unread conversations, mark them read, and exit.")
	fmt.Println("  slackie read --wait")
	fmt.Println("    Run the normal unread scan first. If none exist, wait for the next")
	fmt.Println("    newly arrived message, print it, mark it read, and exit.")
	fmt.Println()
	fmt.Println("Targets:")
	fmt.Println("  #channel-name")
	fmt.Println("  C12345678")
	fmt.Println("  @alice")
	fmt.Println("  #channel-name:1740000000.123456")
	fmt.Println("  C12345678:1740000000.123456")
}
