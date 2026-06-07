package main

import (
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
	default:
		printRelayHelp()
		osExit(ExitConfigError)
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
		handleRelayDaemonStatus()
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
	bg := fs.Bool("daemon", false, "run in background")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file (enables HTTPS/WSS)")
	tlsKey := fs.String("tls-key", "", "TLS private key file")
	fs.Parse(args)

	if *bg {
		childArgs := []string{"relay", "daemon", "start", "-port", fmt.Sprintf("%d", *port)}
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

	fmt.Printf("Starting relay on port %d...\n", *port)
	if *tlsCert != "" && *tlsKey != "" {
		startRelayTLS(*port, *tlsCert, *tlsKey)
	} else {
		startRelay(*port)
	}
}

func handleRelayDaemonStop() {
	if err := stopBackground(relayPidFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
	fmt.Println("Relay daemon stopped")
}

func handleRelayDaemonStatus() {
	statusBackground(relayPidFile)
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
		handleDaemonStop()
	case "status":
		handleDaemonStatus()
	case "systemd":
		handleDaemonSystemdSubcommand(args[1:])
	default:
		printDaemonHelp()
		osExit(ExitConfigError)
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
			osExit(classifyError(err))
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
		osExit(classifyError(err))
	}
	fmt.Println("Daemon stopped")
}

func handleDaemonStatus() {
	statusBackground(daemonPidFile)
}
