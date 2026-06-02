package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
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
	default:
		printPairHelp()
		os.Exit(1)
	}
}

func handlePairListen(args []string) {
	fs := flag.NewFlagSet("pair listen", flag.ExitOnError)
	name := fs.String("name", "", "name to assign to the new target (falls back to remote hostname)")
	timeoutSec := fs.Int("timeout", 300, "seconds to wait for peer to connect (default 5 min)")
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

	code := generateShortCode()

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
		// The daemon registers on the relay using msg.Hostname as its name.
		// We must save a target entry using that same hostname so the relay can route commands.
		// If the user supplied --name, we save an alias too (same token, different key).
		remoteHostname := msg.Hostname
		if remoteHostname == "" {
			remoteHostname = "peer-" + code[:4]
		}

		// Always save under remote hostname (matches daemon's relay-registered name)
		if err := addTarget(remoteHostname, msg.Token); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving target: %v\n", err)
			os.Exit(1)
		}

		targetName := remoteHostname
		// If caller specified a custom alias, save it with RelayName pointing to the real hostname
		if *name != "" && *name != remoteHostname {
			if err := addTargetWithRelayName(*name, msg.Token, remoteHostname); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save alias %q: %v\n", *name, err)
			} else {
				targetName = *name
				fmt.Printf("\nPeer connected! Target %q added (relay name: %s)\n", targetName, remoteHostname)
				fmt.Printf("Run: remotecmd-cli --target %s --cmd 'hostname'\n", targetName)
				return
			}
		}

		fmt.Printf("\nPeer connected! Target %q added\n", targetName)
		fmt.Printf("Run: remotecmd-cli --target %s --cmd 'hostname'\n", targetName)
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

func printPairHelp() {
	fmt.Println(`Usage: remotecmd-cli pair <command>

Commands:
  listen [--name <n>] [--timeout <s>]   Wait for peer to pair; prints one-liner to share`)
}
