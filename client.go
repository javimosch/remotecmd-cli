package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

func handleExec(target, cmd string, timeout int) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Relay.URL == "" {
		return fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	tgt, ok := cfg.Targets[target]
	if !ok {
		return fmt.Errorf("unknown target %q. Run: remotecmd-cli add-target --name %s --token <token>", target, target)
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("connect to relay: %w", err)
	}
	defer conn.Close()

	id := newID()
	req := &Message{
		Type:    "execute",
		ID:      id,
		Target:  target,
		Token:   tgt.Token,
		Cmd:     cmd,
		Timeout: timeout,
	}

	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- fmt.Errorf("read response: %w", err)
				return
			}
			if msg.ID == id && msg.Type == "result" {
				resultCh <- &msg
				return
			}
		}
	}()

	select {
	case result := <-resultCh:
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
		return nil
	case err := <-errCh:
		return err
	case <-time.After(time.Duration(timeout+5) * time.Second):
		return fmt.Errorf("timed out waiting for response from %q", target)
	}
}
