package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// handleTunnel handles the `remotecmd-cli tunnel` command.
func handleTunnel(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "start":
			handleTunnelStart(args[1:])
			return
		case "stop":
			handleTunnelStop(args[1:])
			return
		case "status":
			handleTunnelStatus()
			return
		case "--help", "-h":
			printTunnelHelp()
			return
		}
	}
	// Default: "tunnel --target ... --local ... --remote ... [-daemon]"
	handleTunnelStart(args)
}

func handleTunnelStart(args []string) {
	fs := flag.NewFlagSet("tunnel start", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	local := fs.String("local", "", "local port to listen on (e.g. 5432)")
	remote := fs.String("remote", "", "remote address on target to forward to (e.g. localhost:5432)")
	bg := fs.Bool("daemon", false, "run in background")
	fs.Parse(args)

	if *target == "" || *local == "" || *remote == "" {
		printTunnelHelp()
		osExit(ExitConfigError)
	}

	if *bg {
		pidFile := tunnelPidFile(*target, *local)
		logFile := tunnelLogFile(*target, *local)
		childArgs := []string{"tunnel", "start", "--target", *target, "--local", *local, "--remote", *remote}
		if err := startBackground(pidFile, logFile, childArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(classifyError(err))
		}
		pid := readPid(pidFile)
		fmt.Printf("Tunnel started (PID %d): 127.0.0.1:%s -> %s:%s\n", pid, *local, *target, *remote)
		return
	}

	if err := runTunnel(*target, *local, *remote); err != nil {
		fmt.Fprintf(os.Stderr, "Tunnel error: %v\n", err)
		osExit(ExitInternal)
	}
}

func handleTunnelStop(args []string) {
	fs := flag.NewFlagSet("tunnel stop", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	local := fs.String("local", "", "local port")
	fs.Parse(args)

	if *target == "" || *local == "" {
		printTunnelHelp()
		osExit(ExitConfigError)
	}

	pidFile := tunnelPidFile(*target, *local)
	if err := stopBackground(pidFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	fmt.Printf("Tunnel %s:%s stopped\n", *target, *local)
}

func handleTunnelStatus() {
	dir := tunnelPidsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("No running tunnels")
		return
	}

	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		pidFile := filepath.Join(dir, e.Name())
		running, pid := isRunning(pidFile)
		if running {
			found = true
			fmt.Printf("%s (PID %d)\n", strings.TrimSuffix(e.Name(), ".pid"), pid)
		} else {
			os.Remove(pidFile)
		}
	}
	if !found {
		fmt.Println("No running tunnels")
	}
}

func tunnelPidsDir() string {
	dir := filepath.Join(configDir(), "tunnel-pids")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func tunnelLogsDir() string {
	dir := filepath.Join(configDir(), "tunnel-logs")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func tunnelPidFile(target, local string) string {
	return filepath.Join(tunnelPidsDir(), fmt.Sprintf("%s-%s.pid", target, local))
}

func tunnelLogFile(target, local string) string {
	return filepath.Join(tunnelLogsDir(), fmt.Sprintf("%s-%s.log", target, local))
}

func printTunnelHelp() {
	fmt.Println(`Usage: remotecmd-cli tunnel <command> [options]

Start a TCP tunnel through the WebSocket relay:
  remotecmd-cli tunnel --target <name> --local <port> --remote <host:port> [-daemon]
  remotecmd-cli tunnel start --target <name> --local <port> --remote <host:port> [-daemon]

Manage background tunnels:
  remotecmd-cli tunnel stop  --target <name> --local <port>    Stop a background tunnel
  remotecmd-cli tunnel status                                   List running background tunnels

Examples:
  remotecmd-cli tunnel --target rbm21 --local 18088 --remote 127.0.0.1:8787
  remotecmd-cli tunnel --target rbm21 --local 18088 --remote 127.0.0.1:8787 -daemon
  remotecmd-cli tunnel stop --target rbm21 --local 18088
  remotecmd-cli tunnel status`)
}
