package main

import (
	"fmt"
	"os"
	"strings"
)

const Version = "1.2.0"

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	first := os.Args[1]

	if strings.HasPrefix(first, "--") {
		handleExecFlags(os.Args[1:])
		return
	}

	switch first {
	case "add-target":
		handleAddTarget(os.Args[2:])
	case "remove-target":
		handleRemoveTarget(os.Args[2:])
	case "list-targets":
		handleListTargets()
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
	case "cp":
		handleCP(os.Args[2:])
	case "exec":
		handleExecSubcommand(os.Args[2:])
	case "group":
		handleGroupSubcommand(os.Args[2:])
	case "version":
		fmt.Println("remotecmd-cli version", Version)
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", first)
		printHelp()
		os.Exit(ExitConfigError)
	}
}
