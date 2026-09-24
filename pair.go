package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func handlePairSubcommand(args []string) {
	if len(args) < 1 {
		failUsage("missing_argument", "pair needs a subcommand", printPairHelp)
	}
	switch args[0] {
	case "listen":
		handlePairListen(args[1:])
	case "accept":
		handlePairAccept(args[1:])
	case "disconnect":
		handlePairDisconnect(args[1:])
	default:
		failUsage("unknown_command", "unknown pair subcommand: "+args[0], printPairHelp)
	}
}

func handlePairListen(args []string) {
	fs := flag.NewFlagSet("pair listen", flag.ExitOnError)
	name := fs.String("name", "", "name to assign to the new target (falls back to remote hostname)")
	timeoutSec := fs.Int("timeout", 300, "seconds to wait for peer to connect (default 5 min)")
	codeFlag := fs.String("code", "", "specific pair code to listen for (default: auto-generate)")
	requireActivationKey := fs.Bool("require-activation-key", false, "require the joining daemon to present a valid activation key")
	fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		failErrCode(ExitConfigError, fmt.Errorf("loading config: %w", err))
	}
	if cfg.Relay.URL == "" {
		fail(ExitConfigError, "not_configured", "relay not configured", "remotecmd-cli set-relay --url <url> --name <name>")
	}

	code := *codeFlag
	if code == "" {
		code = generateShortCode()
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := dialRelay(u)
	if err != nil {
		failErrCode(ExitRelayError, fmt.Errorf("connecting to relay: %w", err))
	}
	defer conn.Close()

	if err := conn.WriteJSON(&Message{Type: "pair_listen", Code: code, RequireActivationKey: *requireActivationKey}); err != nil {
		failErrCode(ExitRelayError, fmt.Errorf("sending pair_listen: %w", err))
	}

	oneLiner := fmt.Sprintf(
		"curl -sSL https://raw.githubusercontent.com/javimosch/remotecmd-cli/master/install.sh | sh -s -- --relay %s --code %s",
		cfg.Relay.URL, code,
	)

	fmt.Println("Waiting for peer to connect...")
	fmt.Println()
	fmt.Println("Send this one-liner to your peer:")
	fmt.Println()
	fmt.Printf("  %s\n", oneLiner)
	fmt.Println()
	if *requireActivationKey {
		fmt.Println("Activation key required — the joining daemon must pass --activation-key.")
		fmt.Println()
	}
	fmt.Printf("Listening for pair code %s (timeout: %ds)...\n", code, *timeoutSec)

	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}
			if msg.Type == "pair_done" && msg.Code == code {
				resultCh <- &msg
				return
			}
		}
	}()

	select {
	case msg := <-resultCh:
		remoteHostname := msg.Hostname
		if remoteHostname == "" {
			remoteHostname = "peer-" + code[:4]
		}

		if *name != "" && *name != remoteHostname {
			// User specified an alias — save only the alias entry with RelayName
			// (no raw hostname entry to avoid duplicates like "prod" + "prod-server-01")
			if err := addTargetWithRelayName(*name, msg.Token, remoteHostname); err != nil {
				failErrCode(ExitConfigError, fmt.Errorf("saving target: %w", err))
			}
			fmt.Printf("\nPeer connected! Target %q added (relay name: %s)\n", *name, remoteHostname)
			fmt.Printf("Run: remotecmd-cli --target %s --cmd 'hostname'\n", *name)
		} else {
			// No alias — save under the remote hostname directly
			if err := addTarget(remoteHostname, msg.Token); err != nil {
				failErrCode(ExitConfigError, fmt.Errorf("saving target: %w", err))
			}
			targetName := remoteHostname
			if *name != "" {
				targetName = *name
			}
			fmt.Printf("\nPeer connected! Target %q added\n", targetName)
			fmt.Printf("Run: remotecmd-cli --target %s --cmd 'hostname'\n", targetName)
		}
	case err := <-errCh:
		failErrCode(ExitRelayError, fmt.Errorf("connection error: %w", err))
	case <-time.After(time.Duration(*timeoutSec) * time.Second):
		fail(ExitConfigError, "timeout", fmt.Sprintf("timed out waiting for peer after %ds", *timeoutSec),
			"run the printed one-liner on the new machine, or retry with a longer --timeout")
	}
}

func generateShortCode() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func handlePairAccept(args []string) {
	fs := flag.NewFlagSet("pair accept", flag.ExitOnError)
	codeFlag := fs.String("code", "", "pair code to accept (required)")
	activationKey := fs.String("activation-key", "", "activation key (required if listener set --require-activation-key)")
	fs.Parse(args)

	if *codeFlag == "" {
		fail(ExitConfigError, "missing_argument", "--code is required",
			"remotecmd-cli pair accept --code <code> [--activation-key <key>]")
	}

	// Save the pair code to disk
	if err := savePairCode(*codeFlag); err != nil {
		failErrCode(ExitConfigError, fmt.Errorf("could not save pair code: %w", err))
	}
	fmt.Printf("Pair code %q saved to %s\n", *codeFlag, pairCodePath())

	// Save the activation key to disk (if provided) so the daemon can send it
	if *activationKey != "" {
		if err := saveActivationKey(*activationKey); err != nil {
			failErrCode(ExitConfigError, fmt.Errorf("could not save activation key: %w", err))
		}
	} else {
		deleteActivationKey()
	}

	// Signal the running daemon to re-check the pair code immediately
	pidData, err := os.ReadFile(daemonPidFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: daemon PID file not found (daemon not running?)")
		fmt.Fprintln(os.Stderr, "The pair code will be picked up automatically within 15 seconds when the daemon starts.")
		return
	}

	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &pid); err != nil || pid == 0 {
		fmt.Fprintln(os.Stderr, "Warning: could not parse daemon PID from", daemonPidFile)
		fmt.Fprintln(os.Stderr, "The pair code will be picked up automatically within 15 seconds.")
		return
	}

	if err := sendPairSignal(pid); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not signal daemon (PID %d): %v\n", pid, err)
		fmt.Fprintln(os.Stderr, "The pair code will be picked up automatically within 15 seconds.")
		return
	}

	fmt.Printf("Daemon (PID %d) signaled — pair code sent to relay.\n", pid)
}

func printPairHelp() {
	fmt.Fprintln(helpWriter(), `Usage: remotecmd-cli pair <command>

Commands:
  listen [--name <n>] [--timeout <s>] [--code <c>] [--require-activation-key]
      Wait for peer to pair; prints one-liner to share

  accept --code <c> [--activation-key <key>]
      Accept a pair code on this machine (signals running daemon)

  disconnect --target <name>
      Disconnect a paired target (sends disconnect via relay, daemon exits cleanly)`)
}
