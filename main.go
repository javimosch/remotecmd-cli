package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const Version = "1.0.0"

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
	case "version":
		fmt.Println("remotecmd-cli version", Version)
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", first)
		printHelp()
		os.Exit(1)
	}
}

func handleExecFlags(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	cmd := fs.String("cmd", "", "command to execute")
	timeout := fs.Int("timeout", 30, "command timeout in seconds")
	fs.Parse(args)

	if *target == "" || *cmd == "" {
		fmt.Fprintln(os.Stderr, "Error: --target and --cmd are required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli --target <name> --cmd <command> [--timeout <seconds>]")
		os.Exit(1)
	}

	if err := handleExec(*target, *cmd, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleAddTarget(args []string) {
	fs := flag.NewFlagSet("add-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	token := fs.String("token", "", "auth token")
	fs.Parse(args)

	if *name == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --token are required")
		os.Exit(1)
	}

	if err := addTarget(*name, *token); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Target %q added\n", *name)
}

func handleRemoveTarget(args []string) {
	fs := flag.NewFlagSet("remove-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		os.Exit(1)
	}

	if err := removeTarget(*name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Target %q removed\n", *name)
}

func handleListTargets() {
	if err := listTargets(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleSetRelay(args []string) {
	fs := flag.NewFlagSet("set-relay", flag.ExitOnError)
	url := fs.String("url", "", "relay URL (e.g. http://dk1:3032)")
	name := fs.String("name", "", "this node's name on the relay")
	fs.Parse(args)

	if *url == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --url and --name are required")
		os.Exit(1)
	}

	if err := setRelay(*url, *name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Relay configured: %s (as %q)\n", *url, *name)
}

func handleRelaySubcommand(args []string) {
	if len(args) < 1 {
		printRelayHelp()
		os.Exit(1)
	}
	switch args[0] {
	case "daemon":
		handleRelayDaemon(args[1:])
	default:
		printRelayHelp()
		os.Exit(1)
	}
}

func handleRelayDaemon(args []string) {
	if len(args) < 1 {
		printRelayDaemonHelp()
		os.Exit(1)
	}
	switch args[0] {
	case "start":
		handleRelayDaemonStart(args[1:])
	case "stop":
		handleRelayDaemonStop()
	case "status":
		handleRelayDaemonStatus()
	default:
		printRelayDaemonHelp()
		os.Exit(1)
	}
}

func handleRelayDaemonStart(args []string) {
	fs := flag.NewFlagSet("relay daemon start", flag.ExitOnError)
	port := fs.Int("port", 3032, "relay listen port")
	bg := fs.Bool("daemon", false, "run in background")
	fs.Parse(args)

	if *bg {
		err := startBackground(relayPidFile, relayLogFile,
			"relay", "daemon", "start",
			"-port", fmt.Sprintf("%d", *port))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		pid := readPid(relayPidFile)
		fmt.Printf("Relay daemon started on port %d (PID %d)\n", *port, pid)
		return
	}

	fmt.Printf("Starting relay on port %d...\n", *port)
	startRelay(*port)
}

func handleRelayDaemonStop() {
	if err := stopBackground(relayPidFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Relay daemon stopped")
}

func handleRelayDaemonStatus() {
	statusBackground(relayPidFile)
}

func handleDaemonSubcommand(args []string) {
	if len(args) < 1 {
		printDaemonHelp()
		os.Exit(1)
	}
	switch args[0] {
	case "start":
		handleDaemonStart(args[1:])
	case "stop":
		handleDaemonStop()
	case "status":
		handleDaemonStatus()
	default:
		printDaemonHelp()
		os.Exit(1)
	}
}

func handleDaemonStart(args []string) {
	fs := flag.NewFlagSet("daemon start", flag.ExitOnError)
	token := fs.String("token", "", "auth token (auto-generated if omitted)")
	bg := fs.Bool("daemon", false, "run in background")
	fs.Parse(args)

	if *bg {
		childArgs := []string{"daemon", "start"}
		if *token != "" {
			childArgs = append(childArgs, "-token", *token)
		}
		err := startBackground(daemonPidFile, daemonLogFile, childArgs...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		pid := readPid(daemonPidFile)
		fmt.Printf("Daemon started (PID %d)\n", pid)
		return
	}

	actualToken := *token
	if actualToken == "" {
		existing, err := loadToken()
		if err == nil && existing != "" {
			actualToken = existing
		} else {
			actualToken = generateToken()
			if err := saveToken(actualToken); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save token: %v\n", err)
			}
			fmt.Printf("Generated token: %s\n", actualToken)
		}
	}

	runDaemon(actualToken)
}

func handleDaemonStop() {
	if err := stopBackground(daemonPidFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Daemon stopped")
}

func handleDaemonStatus() {
	statusBackground(daemonPidFile)
}

func printHelp() {
	fmt.Println(`remotecmd-cli — remote command execution via WebSocket relay

EXECUTE:
  remotecmd-cli --target <name> --cmd <command>  Execute command on remote target

CONFIGURATION:
  remotecmd-cli add-target --name <n> --token <t>    Add a known target
  remotecmd-cli remove-target --name <n>              Remove a target
  remotecmd-cli list-targets                          List configured targets
  remotecmd-cli set-relay --url <u> --name <n>        Configure relay connection

RELAY (run on relay hub machine, e.g. dk1):
  remotecmd-cli relay daemon start [--port 3032]     Start relay hub (foreground)
  remotecmd-cli relay daemon start --port 3032 -daemon  Start relay hub (background)
  remotecmd-cli relay daemon stop                    Stop relay hub
  remotecmd-cli relay daemon status                  Check relay hub status

DAEMON (run on target machine, e.g. p22):
  remotecmd-cli daemon start [--token <t>]            Start target daemon (foreground)
  remotecmd-cli daemon start --token <t> -daemon       Start target daemon (background)
  remotecmd-cli daemon stop                           Stop target daemon
  remotecmd-cli daemon status                         Check target daemon status

OTHER:
  remotecmd-cli version    Show version
  remotecmd-cli help       Show this help`)
}

func printRelayHelp() {
	fmt.Println(`Usage: remotecmd-cli relay <command>

Commands:
  daemon    Manage relay daemon (start/stop/status)`)
}

func printRelayDaemonHelp() {
	fmt.Println(`Usage: remotecmd-cli relay daemon <command>

Commands:
  start [--port <n>] [-daemon]  Start relay hub
  stop                           Stop relay hub
  status                         Check relay hub status`)
}

func printDaemonHelp() {
	fmt.Println(`Usage: remotecmd-cli daemon <command>

Commands:
  start [--token <t>] [-daemon]  Start target daemon
  stop                            Stop target daemon
  status                          Check target daemon status`)
}
