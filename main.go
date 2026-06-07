package main

import (
	"flag"
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
		os.Exit(1)
	}
}

func handleExecFlags(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	cmd := fs.String("cmd", "", "command to execute")
	timeout := fs.Int("timeout", 30, "command timeout in seconds")
	stream := fs.Bool("stream", false, "stream output in real time")
	fs.Parse(args)

	if *target == "" || *cmd == "" {
		fmt.Fprintln(os.Stderr, "Error: --target and --cmd are required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli --target <name> --cmd <command> [--timeout <seconds>] [--stream]")
		os.Exit(1)
	}

	if err := handleExec(*target, *cmd, *timeout, *stream); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleExecSubcommand(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	target := fs.String("target", "", "single target machine name")
	targets := fs.String("targets", "", "comma-separated target names")
	group := fs.String("group", "", "target group name")
	cmd := fs.String("cmd", "", "command to execute")
	timeout := fs.Int("timeout", 30, "command timeout in seconds")
	stream := fs.Bool("stream", false, "stream output (single target only)")
	parallel := fs.Int("parallel", 0, "max parallel targets (multi-target only)")
	format := fs.String("format", "table", "output format: json or table (multi-target only)")
	fs.Parse(args)

	if *cmd == "" {
		fmt.Fprintln(os.Stderr, "Error: --cmd is required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli exec --cmd <command> [--target <name> | --targets <list> | --group <name>] [--timeout <s>] [--stream] [--format json|table]")
		os.Exit(1)
	}

	// Determine targets
	var targetList []string
	isMulti := false

	if *group != "" {
		var err error
		targetList, err = resolveTargets(*group, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		isMulti = len(targetList) > 1
	} else if *targets != "" {
		var err error
		targetList, err = resolveTargets(*targets, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		isMulti = len(targetList) > 1
	} else if *target != "" {
		targetList = []string{*target}
		// Validate target exists
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if _, ok := cfg.Targets[*target]; !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown target %q\n", *target)
			os.Exit(1)
		}
		isMulti = false
	} else {
		fmt.Fprintln(os.Stderr, "Error: one of --target, --targets, or --group is required")
		os.Exit(1)
	}

	_ = parallel // Available for future use

	if isMulti {
		if *stream {
			fmt.Fprintln(os.Stderr, "Warning: --stream is not supported for multi-target; ignoring")
		}
		if err := handleMultiExec(targetList, *cmd, *timeout, *format); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := handleExec(targetList[0], *cmd, *timeout, *stream); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

func handleGroupSubcommand(args []string) {
	if len(args) < 1 {
		printGroupHelp()
		os.Exit(1)
	}
	switch args[0] {
	case "create":
		handleGroupCreate(args[1:])
	case "delete":
		handleGroupDelete(args[1:])
	case "add":
		handleGroupAdd(args[1:])
	case "remove":
		handleGroupRemove(args[1:])
	case "list":
		handleGroupList()
	default:
		printGroupHelp()
		os.Exit(1)
	}
}

func handleGroupCreate(args []string) {
	fs := flag.NewFlagSet("group create", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	targets := fs.String("targets", "", "comma-separated target names")
	fs.Parse(args)

	if *name == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --targets are required")
		os.Exit(1)
	}

	list := strings.Split(*targets, ",")
	for i, t := range list {
		list[i] = strings.TrimSpace(t)
	}

	if err := groupCreate(*name, list); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Group %q created with %d targets\n", *name, len(list))
}

func handleGroupDelete(args []string) {
	fs := flag.NewFlagSet("group delete", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		os.Exit(1)
	}

	if err := groupDelete(*name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Group %q deleted\n", *name)
}

func handleGroupAdd(args []string) {
	fs := flag.NewFlagSet("group add", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	targets := fs.String("targets", "", "comma-separated target names")
	fs.Parse(args)

	if *name == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --targets are required")
		os.Exit(1)
	}

	list := strings.Split(*targets, ",")
	for i, t := range list {
		list[i] = strings.TrimSpace(t)
	}

	if err := groupAddTargets(*name, list); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Targets added to group %q\n", *name)
}

func handleGroupRemove(args []string) {
	fs := flag.NewFlagSet("group remove", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	targets := fs.String("targets", "", "comma-separated target names")
	fs.Parse(args)

	if *name == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --targets are required")
		os.Exit(1)
	}

	list := strings.Split(*targets, ",")
	for i, t := range list {
		list[i] = strings.TrimSpace(t)
	}

	if err := groupRemoveTargets(*name, list); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Targets removed from group %q\n", *name)
}

func handleGroupList() {
	if err := groupList(); err != nil {
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

EXECUTE (single target):
  remotecmd-cli --target <name> --cmd <command> [--timeout <s>] [--stream]    Single target (legacy)
  remotecmd-cli exec --target <name> --cmd <command> [--timeout <s>] [--stream]  Single target (new)

EXECUTE (multi-target):
  remotecmd-cli exec --targets <t1,t2,...> --cmd <command> [--timeout <s>] [--format json|table]
  remotecmd-cli exec --group <name> --cmd <command> [--timeout <s>] [--format json|table]

FILE TRANSFER:
  remotecmd-cli cp --target <name> --src <path> --dst <path>  Copy file or directory to remote target

TARGET CONFIGURATION:
  remotecmd-cli add-target --name <n> --token <t>    Add a known target
  remotecmd-cli remove-target --name <n>              Remove a target
  remotecmd-cli list-targets                          List configured targets and groups
  remotecmd-cli set-relay --url <u> --name <n>        Configure relay connection

GROUP MANAGEMENT:
  remotecmd-cli group create --name <n> --targets <t1,t2,...>  Create a target group
  remotecmd-cli group add --name <n> --targets <t1,t2,...>     Add targets to a group
  remotecmd-cli group remove --name <n> --targets <t1,t2,...>  Remove targets from a group
  remotecmd-cli group delete --name <n>                        Delete a group
  remotecmd-cli group list                                     List all groups

ALIAS:
  remotecmd-cli alias install                         Install convenience aliases (rc, rcx, rcl, rcs, rcc)
  remotecmd-cli alias uninstall                       Remove installed aliases

RELAY (run on relay hub machine):
  remotecmd-cli relay daemon start [--port 3032]     Start relay hub (foreground)
  remotecmd-cli relay daemon start --port 3032 -daemon  Start relay hub (background)
  remotecmd-cli relay daemon stop                    Stop relay hub
  remotecmd-cli relay daemon status                  Check relay hub status

DAEMON (run on target machine):
  remotecmd-cli daemon start [--token <t>]            Start target daemon (foreground)
  remotecmd-cli daemon start --token <t> -daemon       Start target daemon (background)
  remotecmd-cli daemon stop                           Stop target daemon
  remotecmd-cli daemon status                         Check target daemon status

PAIRING:
  remotecmd-cli pair listen [--name <n>] [--timeout <s>] [--code <c>]  Wait for peer; prints one-liner
  remotecmd-cli pair accept --code <c>                                   Accept a pair code

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

func printGroupHelp() {
	fmt.Println(`Usage: remotecmd-cli group <command>

Commands:
  create --name <n> --targets <t1,t2,...>   Create a target group
  delete --name <n>                          Delete a group
  add --name <n> --targets <t1,t2,...>       Add targets to a group
  remove --name <n> --targets <t1,t2,...>    Remove targets from a group
  list                                        List all groups`)
}
