package main

import (
	"errors"
	"flag"
	"fmt"
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
		fail(ExitConfigError, "missing_argument", "--target is required", "remotecmd-cli pair disconnect --target <name>")
	}

	cfg, err := loadConfig()
	if err != nil {
		failErrCode(ExitConfigError, fmt.Errorf("loading config: %w", err))
	}
	if cfg.Relay.URL == "" {
		fail(ExitConfigError, "not_configured", "relay not configured", "remotecmd-cli set-relay --url <url> --name <name>")
	}

	tgt, ok := cfg.Targets[*target]
	if !ok {
		fail(ExitConfigError, "unknown_target", fmt.Sprintf("target %q not found in config", *target),
			defaultSuggestions["unknown_target"]...)
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := dialRelay(u)
	if err != nil {
		failErrCode(ExitRelayError, fmt.Errorf("connecting to relay: %w", err))
	}
	defer conn.Close()

	// Send disconnect message targeting the daemon
	disconnectMsg := &Message{
		Type:   "disconnect",
		Target: *target,
		Token:  tgt.Token,
	}
	if err := conn.WriteJSON(disconnectMsg); err != nil {
		failErrCode(ExitRelayError, fmt.Errorf("sending disconnect: %w", err))
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
			failErrCode(ExitConfigError, errors.New(msg.Error))
		}
		fmt.Printf("Target %q disconnected.\n", *target)
	case err := <-errCh:
		failErrCode(ExitRelayError, fmt.Errorf("connection error: %w", err))
	case <-time.After(10 * time.Second):
		fail(ExitRelayError, "timeout", "timed out waiting for disconnect confirmation (target may already be offline)",
			"remotecmd-cli list-targets --refresh")
	}
}
