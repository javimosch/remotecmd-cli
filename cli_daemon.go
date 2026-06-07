package main

import (
	"flag"
	"fmt"
	"os"
)

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
