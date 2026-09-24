package main

import (
	"fmt"
	"os"
	"strings"
)

const Version = "2.5.0"

func main() {
	if len(os.Args) < 2 {
		printHelp()
		fail(ExitConfigError, "missing_argument", "no command given",
			"remotecmd-cli guide", "remotecmd-cli help-json")
	}

	first := os.Args[1]

	if first == "--help-json" {
		handleHelpJSON()
		return
	}

	if strings.HasPrefix(first, "--") {
		maybeNudge()
		handleExecFlags(os.Args[1:])
		return
	}

	// Nudge on server-hitting commands (not on local-only ones)
	switch first {
	case "exec", "cp", "list-targets", "tunnel", "client":
		maybeNudge()
	}

	switch first {
	case "add-target":
		handleAddTarget(os.Args[2:])
	case "remove-target":
		handleRemoveTarget(os.Args[2:])
	case "list-targets":
		handleListTargets(os.Args[2:])
	case "set-relay":
		handleSetRelay(os.Args[2:])
	case "relay":
		handleRelaySubcommand(os.Args[2:])
	case "daemon":
		handleDaemonSubcommand(os.Args[2:])
	case "alias":
		handleAliasSubcommand(os.Args[2:])
	case "pair":
		handlePairSubcommand(os.Args[2:])
	case "sidecar":
		handleSidecarSubcommand(os.Args[2:])
	case "cp":
		handleCP(os.Args[2:])
	case "exec":
		handleExecSubcommand(os.Args[2:])
	case "group":
		handleGroupSubcommand(os.Args[2:])
	case "client":
		handleClientSubcommand(os.Args[2:])
	case "tunnel":
		handleTunnel(os.Args[2:])
	case "feedback":
		handleFeedback(os.Args[2:])
	case "mcp":
		handleMCP(os.Args[2:])
	case "update":
		handleUpdate(os.Args[2:])
	case "version":
		if len(os.Args) > 2 && os.Args[2] == "--json" {
			fmt.Printf("{\"tool\":\"remotecmd-cli\",\"version\":%q}\n", Version)
			return
		}
		fmt.Println("remotecmd-cli version", Version)
	case "guide":
		handleGuide(os.Args[2:])
	case "help-json":
		handleHelpJSON()
	case "help", "--help", "-h":
		printHelp()
	default:
		fail(ExitConfigError, "unknown_command", "unknown command: "+first,
			"remotecmd-cli help-json", "remotecmd-cli guide")
	}
}
