package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type TargetDaemon struct {
	relayURL  string
	name      string
	token     string
	conn      *websocket.Conn
	writeMu   sync.Mutex

	reMu       sync.Mutex
	reassembly map[string]*fileReassembly
	// pendingBinaryChunk tracks the chunk header that precedes a binary frame
	pendingBinaryChunk *fileReassembly
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

	// Listen for SIGUSR1 — used by "pair accept" to trigger immediate pair code re-check
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	go func() {
		for range sigCh {
			log.Printf("Received SIGUSR1 — re-checking pair code")
			td.sendPairIfNeeded()
		}
	}()

	for {
		td.run()
		log.Printf("Disconnected. Reconnecting in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func (td *TargetDaemon) run() {
	conn, _, err := dialRelay(td.relayURL)
	if err != nil {
		log.Printf("Connection failed: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(relayMaxFrameSize)
	td.conn = conn

	// Stop channel for the pair retry goroutine; closed when run() returns
	pairRetryStop := make(chan struct{})
	defer close(pairRetryStop)

	td.send(&Message{
		Type:  "register",
		Name:  td.name,
		Token: td.token,
	})

	for {
		// Read raw message to handle both text (JSON) and binary frames
		msgType, rawData, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("Read error: %v", err)
			}
			return
		}

		// Binary frames are file chunk payloads — append to the pending reassembly
		if msgType == websocket.BinaryMessage {
			td.handleBinaryChunk(rawData)
			continue
		}

		var msg Message
		if err := json.Unmarshal(rawData, &msg); err != nil {
			log.Printf("JSON unmarshal error: %v", err)
			continue
		}

		switch msg.Type {
		case "registered":
			log.Printf("Registered as %q", msg.Name)
			go td.sendPairIfNeeded()
			go td.pairRetryLoop(pairRetryStop)

		case "command":
			log.Printf("Received command (id=%s, stream=%v): %s", msg.ID, msg.Stream, msg.Cmd)
			go td.executeCommand(&msg)

		case "file_transfer":
			log.Printf("Received file transfer (id=%s, mode=%s, chunked=%v): %s -> %s", msg.ID, msg.Mode, msg.Chunked, msg.SrcPath, msg.DstPath)
			if msg.Chunked {
				// Register reassembly state synchronously so the chunks that
				// follow on this connection always find it.
				td.beginChunkedTransfer(&msg)
			} else {
				go td.handleFileTransfer(&msg)
			}

		case "file_chunk":
			// Handled synchronously to preserve chunk ordering.
			td.handleFileChunk(&msg)

		case "pair_confirmed":
			log.Printf("Pair confirmed (code=%s)", msg.Code)
			deletePairCode()
			deleteActivationKey()

		case "tunnel_open":
			go td.handleTunnelOpen(&msg)

		case "tunnel_data":
			td.handleTunnelData(&msg)

		case "tunnel_close":
			td.handleTunnelClose(&msg)

		case "disconnect":
			log.Printf("Received disconnect from relay — exiting cleanly")
			td.send(&Message{Type: "disconnect_ack"})
			os.Exit(0)

		case "error":
			// Suppress pair-related errors — daemon retries automatically
			if strings.HasPrefix(msg.Error, "pair") {
				// expected while waiting for a listener
			} else {
				log.Printf("Relay error: %s", msg.Error)
			}

		default:
			log.Printf("Unknown message type: %s", msg.Type)
		}
	}
}

func (td *TargetDaemon) executeCommand(msg *Message) {
	if msg.Stream {
		td.executeCommandStreaming(msg)
	} else {
		td.executeCommandBuffered(msg)
	}
}

func (td *TargetDaemon) executeCommandBuffered(msg *Message) {
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

	// Forward stdin data if provided (issue #6: pipe stdin to remote command)
	if msg.StdinData != "" {
		stdinBytes, err := base64.StdEncoding.DecodeString(msg.StdinData)
		if err != nil {
			td.send(errResult(msg.ID, "failed to decode stdin data: "+err.Error()))
			return
		}
		cmd.Stdin = bytes.NewReader(stdinBytes)
	}

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

func (td *TargetDaemon) executeCommandStreaming(msg *Message) {
	start := time.Now()

	log.Printf("Executing command (streaming, id=%s): %s", msg.ID, msg.Cmd)

	timeout := msg.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", msg.Cmd)

	// Forward stdin data if provided (issue #6)
	if msg.StdinData != "" {
		stdinBytes, err := base64.StdEncoding.DecodeString(msg.StdinData)
		if err != nil {
			td.send(errResult(msg.ID, "failed to decode stdin data: "+err.Error()))
			return
		}
		cmd.Stdin = bytes.NewReader(stdinBytes)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		td.send(errResult(msg.ID, "failed to create stdout pipe: "+err.Error()))
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		td.send(errResult(msg.ID, "failed to create stderr pipe: "+err.Error()))
		return
	}

	if err := cmd.Start(); err != nil {
		td.send(errResult(msg.ID, "failed to start command: "+err.Error()))
		return
	}

	var wg sync.WaitGroup
	streamPipe := func(pipe io.Reader, streamName string) {
		defer wg.Done()
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			td.send(&Message{
				Type:       "stream_chunk",
				ID:         msg.ID,
				StreamName: streamName,
				Data:       scanner.Text() + "\n",
			})
		}
	}

	wg.Add(2)
	go streamPipe(stdoutPipe, "stdout")
	go streamPipe(stderrPipe, "stderr")
	wg.Wait()

	err = cmd.Wait()
	duration := time.Since(start).Milliseconds()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("Command timed out (id=%s)", msg.ID)
			td.send(streamEndErr(msg.ID, fmt.Sprintf("command timed out after %ds", timeout)))
			return
		}
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		log.Printf("Command failed (id=%s, exit=%d)", msg.ID, exitCode)
		td.send(streamEndOK(msg.ID, exitCode, duration))
		return
	}

	log.Printf("Command succeeded (id=%s, duration=%dms)", msg.ID, duration)
	td.send(streamEndOK(msg.ID, 0, duration))
}

func (td *TargetDaemon) send(msg *Message) {
	td.writeMu.Lock()
	defer td.writeMu.Unlock()
	if td.conn == nil {
		log.Printf("Cannot send message: not connected")
		return
	}
	if err := td.conn.WriteJSON(msg); err != nil {
		log.Printf("Write error: %v", err)
	}
}

func (td *TargetDaemon) sendPairIfNeeded() {
	code, err := loadPairCode()
	if err != nil || code == "" {
		return
	}
	// Use the registered name (td.name) as the hostname in the pair message.
	// This is the name the relay knows the daemon by, so the listener can
	// save it as the relay_name for command routing.
	hostname := td.name
	activationKey, _ := loadActivationKey()
	log.Printf("Sending pair message (code=%s, hostname=%s, hasActivationKey=%v)", code, hostname, activationKey != "")
	td.send(&Message{
		Type:          "pair",
		Code:          code,
		Token:         td.token,
		Hostname:      hostname,
		ActivationKey: activationKey,
	})
	// Don't delete the pair code here — keep retrying until
	// the relay sends pair_confirmed (which deletes it).
	// This handles the edge case where the install script saves
	// a new pair code while the daemon is already running.
	log.Printf("Pair code sent (will retry until confirmed)")
}

// pairRetryLoop retries sending pair messages with exponential backoff.
//
// The retry interval grows over time to avoid spamming the relay when
// pairing is never completed (e.g. operator forgot, relay lost the
// listener, cloud-init hasn't run yet):
//
//	0-5 min:   15s intervals  (normal pairing — common case)
//	5-60 min:  1 min intervals (slow colleague / relay restart)
//	1-24 hours: 5 min intervals (provisioning, "I'll do it tomorrow")
//	after 24h: stop, delete pair code, log "expired"
//
// Total retries in 24h: ~290 (vs 5,760 with flat 15s).
func (td *TargetDaemon) pairRetryLoop(stop chan struct{}) {
	// Use the pair_code file's modification time as the start, so a
	// daemon restart doesn't reset the 24h expiry clock.
	start := fileModTime(pairCodePath())
	if start.IsZero() {
		start = time.Now()
	}
	for {
		elapsed := time.Since(start)

		// After 24 hours, give up and clean up the stale code.
		if elapsed > 24*time.Hour {
			code, _ := loadPairCode()
			if code != "" {
				log.Printf("Pair code %s expired after 24h without confirmation — removing", code)
				deletePairCode()
			}
			return
		}

		// Determine interval based on elapsed time.
		var interval time.Duration
		switch {
		case elapsed < 5*time.Minute:
			interval = 15 * time.Second
		case elapsed < time.Hour:
			interval = time.Minute
		default:
			interval = 5 * time.Minute
		}

		select {
		case <-time.After(interval):
			td.sendPairIfNeeded()
		case <-stop:
			return
		}
	}
}


