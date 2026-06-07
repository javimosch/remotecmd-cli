package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

func handlePairSubcommand(args []string) {
	if len(args) < 1 {
		printPairHelp()
		os.Exit(1)
	}
	switch args[0] {
	case "listen":
		handlePairListen(args[1:])
	case "accept":
		handlePairAccept(args[1:])
	default:
		printPairHelp()
		os.Exit(1)
	}
}

func handlePairListen(args []string) {
	fs := flag.NewFlagSet("pair listen", flag.ExitOnError)
	name := fs.String("name", "", "name to assign to the new target (falls back to remote hostname)")
	timeoutSec := fs.Int("timeout", 300, "seconds to wait for peer to connect (default 5 min)")
	codeFlag := fs.String("code", "", "specific pair code to listen for (default: auto-generate)")
	fs.Parse(args)

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	if cfg.Relay.URL == "" {
		fmt.Fprintln(os.Stderr, "Error: relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
		os.Exit(1)
	}

	code := *codeFlag
	if code == "" {
		code = generateShortCode()
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to relay: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := conn.WriteJSON(&Message{Type: "pair_listen", Code: code}); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending pair_listen: %v\n", err)
		os.Exit(1)
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
			// (no raw hostname entry to avoid duplicates like "dk1" + "vpspoly1")
			if err := addTargetWithRelayName(*name, msg.Token, remoteHostname); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving target: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("\nPeer connected! Target %q added (relay name: %s)\n", *name, remoteHostname)
			fmt.Printf("Run: remotecmd-cli --target %s --cmd 'hostname'\n", *name)
		} else {
			// No alias — save under the remote hostname directly
			if err := addTarget(remoteHostname, msg.Token); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving target: %v\n", err)
				os.Exit(1)
			}
			targetName := remoteHostname
			if *name != "" {
				targetName = *name
			}
			fmt.Printf("\nPeer connected! Target %q added\n", targetName)
			fmt.Printf("Run: remotecmd-cli --target %s --cmd 'hostname'\n", targetName)
		}
	case err := <-errCh:
		fmt.Fprintf(os.Stderr, "Connection error: %v\n", err)
		os.Exit(1)
	case <-time.After(time.Duration(*timeoutSec) * time.Second):
		fmt.Fprintf(os.Stderr, "Timed out waiting for peer after %ds\n", *timeoutSec)
		os.Exit(1)
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
	fs.Parse(args)

	if *codeFlag == "" {
		fmt.Fprintln(os.Stderr, "Error: --code is required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli pair accept --code <code>")
		os.Exit(1)
	}

	// Save the pair code to disk
	if err := savePairCode(*codeFlag); err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not save pair code: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pair code %q saved to %s\n", *codeFlag, pairCodePath())

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

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not find daemon process (PID", pid, ")")
		fmt.Fprintln(os.Stderr, "The pair code will be picked up automatically within 15 seconds.")
		return
	}

	if err := proc.Signal(syscall.SIGUSR1); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not signal daemon (PID %d): %v\n", pid, err)
		fmt.Fprintln(os.Stderr, "The pair code will be picked up automatically within 15 seconds.")
		return
	}

	fmt.Printf("Daemon (PID %d) signaled — pair code sent to relay.\n", pid)
}

func printPairHelp() {
	fmt.Println(`Usage: remotecmd-cli pair <command>

Commands:
  listen [--name <n>] [--timeout <s>] [--code <c>]   Wait for peer to pair; prints one-liner to share
  accept --code <c>                                   Accept a pair code on this machine (signals running daemon)`)
}
