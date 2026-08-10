package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// handlePairDisconnect sends a disconnect message to a paired target via the
// relay. The target daemon receives it and exits cleanly. This is the kill
// switch for sidecar-managed nodes — initiated from the localhost that
// originally paired, no second endpoint needed on the sidecar.
func handlePairDisconnect(args []string) {
	fs := flag.NewFlagSet("pair disconnect", flag.ExitOnError)
	target := fs.String("target", "", "target name to disconnect (required)")
	fs.Parse(args)

	if *target == "" {
		fmt.Fprintln(os.Stderr, "Error: --target is required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli pair disconnect --target <name>")
		osExit(ExitConfigError)
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		osExit(ExitConfigError)
	}
	if cfg.Relay.URL == "" {
		fmt.Fprintln(os.Stderr, "Error: relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
		osExit(ExitConfigError)
	}

	tgt, ok := cfg.Targets[*target]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: target %q not found in config\n", *target)
		osExit(ExitConfigError)
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := dialRelay(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to relay: %v\n", err)
		osExit(ExitConfigError)
	}
	defer conn.Close()

	// Send disconnect message targeting the daemon
	disconnectMsg := &Message{
		Type:   "disconnect",
		Target: *target,
		Token:  tgt.Token,
	}
	if err := conn.WriteJSON(disconnectMsg); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending disconnect: %v\n", err)
		osExit(ExitConfigError)
	}

	fmt.Printf("Disconnect sent to %q...\n", *target)

	// Wait for confirmation (disconnect_confirmed) with a short timeout
	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}
			if msg.Type == "disconnect_confirmed" || msg.Type == "error" {
				resultCh <- &msg
				return
			}
		}
	}()

	select {
	case msg := <-resultCh:
		if msg.Type == "error" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", msg.Error)
			osExit(ExitConfigError)
		}
		fmt.Printf("Target %q disconnected.\n", *target)
	case err := <-errCh:
		fmt.Fprintf(os.Stderr, "Connection error: %v\n", err)
		osExit(ExitConfigError)
	case <-time.After(10 * time.Second):
		fmt.Fprintf(os.Stderr, "Timed out waiting for disconnect confirmation (target may already be offline)\n")
		osExit(ExitConfigError)
	}
}
