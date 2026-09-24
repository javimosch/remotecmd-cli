package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func handleRelaySubcommand(args []string) {
	if len(args) < 1 {
		printRelayHelp()
		osExit(ExitConfigError)
	}
	switch args[0] {
	case "daemon":
		handleRelayDaemon(args[1:])
	case "add-key":
		handleRelayAddKey(args[1:])
	case "remove-key":
		handleRelayRemoveKey(args[1:])
	case "list-keys":
		handleRelayListKeys()
	default:
		printRelayHelp()
		osExit(ExitConfigError)
	}
}

func handleRelayAddKey(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli relay add-key <key>")
		osExit(ExitConfigError)
	}
	key := args[0]
	if err := addActivationKey(key); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	fmt.Printf("Activation key added: %s\n", key)
	fmt.Println("The relay will pick it up automatically (no restart needed).")
}

func handleRelayRemoveKey(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli relay remove-key <key>")
		osExit(ExitConfigError)
	}
	key := args[0]
	if err := removeActivationKey(key); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	fmt.Printf("Activation key removed: %s\n", key)
}

func handleRelayListKeys() {
	keys := activationKeys.list()
	if len(keys) == 0 {
		fmt.Println("No activation keys configured.")
		fmt.Printf("File: %s\n", activationKeysPath())
		return
	}
	fmt.Printf("Activation keys (%d) — %s:\n", len(keys), activationKeysPath())
	for _, k := range keys {
		masked := k
		if len(masked) > 8 {
			masked = masked[:4] + "..." + masked[len(masked)-4:]
		}
		fmt.Printf("  %s\n", masked)
	}
}

func handleRelayDaemon(args []string) {
	if len(args) < 1 {
		printRelayDaemonHelp()
		osExit(ExitConfigError)
	}
	switch args[0] {
	case "start":
		handleRelayDaemonStart(args[1:])
	case "stop":
		handleRelayDaemonStop()
	case "status":
		handleRelayDaemonStatus(args[1:])
	case "systemd":
		handleRelaySystemdSubcommand(args[1:])
	default:
		printRelayDaemonHelp()
		osExit(ExitConfigError)
	}
}

func handleRelayDaemonStart(args []string) {
	fs := flag.NewFlagSet("relay daemon start", flag.ExitOnError)
	port := fs.Int("port", 3032, "relay listen port")
	host := fs.String("host", "", "interface to bind (default: all interfaces — a relay must be reachable by remote daemons)")
	bg := fs.Bool("daemon", false, "run in background")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file (enables HTTPS/WSS, or set RCMD_TLS_CERT)")
	tlsKey := fs.String("tls-key", "", "TLS private key file (or set RCMD_TLS_KEY)")
	fs.Parse(args)

	// Fall back to env vars for TLS cert/key
	if *tlsCert == "" {
		*tlsCert = os.Getenv("RCMD_TLS_CERT")
	}
	if *tlsKey == "" {
		*tlsKey = os.Getenv("RCMD_TLS_KEY")
	}

	if *bg {
		childArgs := []string{"relay", "daemon", "start", "-port", fmt.Sprintf("%d", *port)}
		if *host != "" {
			childArgs = append(childArgs, "-host", *host)
		}
		if *tlsCert != "" {
			childArgs = append(childArgs, "-tls-cert", *tlsCert)
		}
		if *tlsKey != "" {
			childArgs = append(childArgs, "-tls-key", *tlsKey)
		}
		err := startBackground(relayPidFile, relayLogFile, childArgs...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(classifyError(err))
		}
		pid := readPid(relayPidFile)
		fmt.Printf("Relay daemon started on port %d (PID %d)\n", *port, pid)
		return
	}

	fmt.Printf("Starting relay on %s...\n", relayListenAddr(*host, *port))
	if *tlsCert != "" && *tlsKey != "" {
		startRelayTLS(*host, *port, *tlsCert, *tlsKey)
	} else {
		startRelay(*host, *port)
	}
}

func handleRelayDaemonStop() {
	if err := stopBackground(relayPidFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	fmt.Println("Relay daemon stopped")
}

func handleRelayDaemonStatus(args []string) {
	fs := flag.NewFlagSet("relay daemon status", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output; exit 3 when stopped (cli-daemon-spec)")
	fs.Parse(args)
	reportStatus(relayPidFile, "", *jsonOut)
}

func handleDaemonSubcommand(args []string) {
	if len(args) < 1 {
		printDaemonHelp()
		osExit(ExitConfigError)
	}
	switch args[0] {
	case "start":
		handleDaemonStart(args[1:])
	case "stop":
		handleDaemonStop(args[1:])
	case "status":
		handleDaemonStatus(args[1:])
	case persistenceSubcommandName():
		handleDaemonPersistenceSubcommand(args[1:])
	default:
		printDaemonHelp()
		osExit(ExitConfigError)
	}
}

func handleDaemonStart(args []string) {
	fs := flag.NewFlagSet("daemon start", flag.ExitOnError)
	token := fs.String("token", "", "auth token (auto-generated if omitted)")
	name := fs.String("name", "", "override relay name from config (run an extra instance)")
	bg := fs.Bool("daemon", false, "run in background")
	fs.Parse(args)

	pidFile := namedDaemonPidFile(*name)
	logFile := daemonLogFile
	if *name != "" {
		logFile += "-" + *name
	}

	if *bg {
		childArgs := []string{"daemon", "start"}
		if *token != "" {
			childArgs = append(childArgs, "-token", *token)
		}
		if *name != "" {
			childArgs = append(childArgs, "-name", *name)
		}
		err := startBackground(pidFile, logFile, childArgs...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(classifyError(err))
		}
		pid := readPid(pidFile)
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

	runDaemon(actualToken, *name)
}

// namedDaemonPidFile keeps each --name instance's PID file apart, so a
// second daemon on the same machine can be started, stopped and queried
// without touching the first.
func namedDaemonPidFile(name string) string {
	if name == "" {
		return daemonPidFile
	}
	return daemonPidFile + "-" + name
}

func handleDaemonStop(args []string) {
	fs := flag.NewFlagSet("daemon stop", flag.ExitOnError)
	name := fs.String("name", "", "instance name (must match the one used at start)")
	fs.Parse(args)
	if err := stopBackground(namedDaemonPidFile(*name)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	fmt.Println("Daemon stopped")
}

func handleDaemonStatus(args []string) {
	fs := flag.NewFlagSet("daemon status", flag.ExitOnError)
	name := fs.String("name", "", "instance name (must match the one used at start)")
	jsonOut := fs.Bool("json", false, "JSON output; exit 3 when stopped (cli-daemon-spec)")
	fs.Parse(args)
	reportStatus(namedDaemonPidFile(*name), *name, *jsonOut)
}

// reportStatus prints a background process's state. Text output keeps the
// legacy behaviour (exit 0 either way) for existing scripts; --json follows
// cli-daemon-spec §4/§5: {"ok":true,"daemon":"running"|"stopped"}, exit 0
// when running and 3 when stopped, so callers can branch on $? alone.
func reportStatus(pidFile, name string, jsonOut bool) {
	if !jsonOut {
		statusBackground(pidFile)
		return
	}
	running, pid := isRunning(pidFile)
	out := map[string]any{"ok": true, "daemon": "stopped", "pid_file": pidFile}
	if name != "" {
		out["name"] = name
	}
	if running {
		out["daemon"] = "running"
		out["pid"] = pid
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	if !running {
		osExit(ExitConfigError)
	}
}
