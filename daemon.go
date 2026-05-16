package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type TargetDaemon struct {
	relayURL  string
	name      string
	token     string
	conn      *websocket.Conn
	writeMu   sync.Mutex
}

func runDaemon(token string) {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if cfg.Relay.URL == "" {
		log.Fatalf("Relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}
	if cfg.Relay.Name == "" {
		log.Fatalf("Node name not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	td := &TargetDaemon{
		relayURL: wsURL(cfg.Relay.URL),
		name:     cfg.Relay.Name,
		token:    token,
	}

	log.Printf("Connecting to relay at %s as %q", td.relayURL, td.name)

	for {
		td.run()
		log.Printf("Disconnected. Reconnecting in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func (td *TargetDaemon) run() {
	conn, _, err := websocket.DefaultDialer.Dial(td.relayURL, nil)
	if err != nil {
		log.Printf("Connection failed: %v", err)
		return
	}
	defer conn.Close()
	td.conn = conn

	td.send(&Message{
		Type:  "register",
		Name:  td.name,
		Token: td.token,
	})

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("Read error: %v", err)
			}
			return
		}

		switch msg.Type {
		case "registered":
			log.Printf("Registered as %q", msg.Name)

		case "command":
			go td.executeCommand(&msg)

		case "error":
			log.Printf("Relay error: %s", msg.Error)

		default:
			log.Printf("Unknown message type: %s", msg.Type)
		}
	}
}

func (td *TargetDaemon) executeCommand(msg *Message) {
	start := time.Now()

	log.Printf("Executing command (id=%s): %s", msg.ID, msg.Cmd)

	timeout := msg.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(timeout)*time.Second)
	defer cancel()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", msg.Cmd)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	duration := time.Since(start).Milliseconds()
	stdout := strings.TrimSpace(stdoutBuf.String())
	stderr := strings.TrimSpace(stderrBuf.String())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("Command timed out (id=%s)", msg.ID)
			td.send(errResult(msg.ID, fmt.Sprintf("command timed out after %ds", timeout)))
			return
		}
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		log.Printf("Command failed (id=%s, exit=%d)", msg.ID, exitCode)
		td.send(okResult(msg.ID, stdout, stderr, exitCode, duration))
		return
	}

	log.Printf("Command succeeded (id=%s, duration=%dms)", msg.ID, duration)
	td.send(okResult(msg.ID, stdout, stderr, 0, duration))
}

func (td *TargetDaemon) send(msg *Message) {
	td.writeMu.Lock()
	defer td.writeMu.Unlock()
	if err := td.conn.WriteJSON(msg); err != nil {
		log.Printf("Write error: %v", err)
	}
}


