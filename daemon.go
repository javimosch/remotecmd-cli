package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	conn, _, err := websocket.DefaultDialer.Dial(td.relayURL, nil)
	if err != nil {
		log.Printf("Connection failed: %v", err)
		return
	}
	defer conn.Close()
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
			go td.sendPairIfNeeded()
			go td.pairRetryLoop(pairRetryStop)

		case "command":
			log.Printf("Received command (id=%s, stream=%v): %s", msg.ID, msg.Stream, msg.Cmd)
			go td.executeCommand(&msg)

		case "file_transfer":
			log.Printf("Received file transfer (id=%s, mode=%s): %s -> %s", msg.ID, msg.Mode, msg.SrcPath, msg.DstPath)
			go td.handleFileTransfer(&msg)

		case "pair_confirmed":
			log.Printf("Pair confirmed (code=%s)", msg.Code)
			deletePairCode()

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

func (td *TargetDaemon) handleFileTransfer(msg *Message) {
	var err error
	var data []byte

	switch msg.Mode {
	case "scp":
		// Decode base64 content
		data, err = base64.StdEncoding.DecodeString(msg.Content)
		if err != nil {
			td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to decode file content: " + err.Error()})
			return
		}

		// Write file
		if err := os.WriteFile(msg.DstPath, data, 0644); err != nil {
			td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to write file: " + err.Error()})
			return
		}

		log.Printf("File transfer succeeded (id=%s): %s -> %s", msg.ID, msg.SrcPath, msg.DstPath)
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(true)})

	case "rsync":
		// Decode base64 content
		data, err = base64.StdEncoding.DecodeString(msg.Content)
		if err != nil {
			td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to decode file content: " + err.Error()})
			return
		}

		// Create destination directory if it doesn't exist
		if err := os.MkdirAll(msg.DstPath, 0755); err != nil {
			td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to create destination directory: " + err.Error()})
			return
		}

		// Extract tar archive
		if err := extractTarArchive(data, msg.DstPath); err != nil {
			td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to extract tar archive: " + err.Error()})
			return
		}

		log.Printf("Directory sync succeeded (id=%s): %s -> %s", msg.ID, msg.SrcPath, msg.DstPath)
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(true)})

	default:
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "unknown file transfer mode: " + msg.Mode})
		return
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func extractTarArchive(tarData []byte, dstPath string) error {
	tr := tar.NewReader(bytes.NewReader(tarData))

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return err
		}

		// Construct destination path
		targetPath := filepath.Join(dstPath, header.Name)

		// Skip symlinks for security
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			log.Printf("Skipping symlink: %s", header.Name)
			continue
		}

		// Create directory
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return err
			}
			continue
		}

		// Create file
		if header.Typeflag == tar.TypeReg {
			// Create parent directory if needed
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}

			// Create file
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			// Copy data
			if _, err := io.Copy(file, tr); err != nil {
				file.Close()
				return err
			}
			file.Close()
		}
	}

	return nil
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
	hostname, _ := os.Hostname()
	log.Printf("Sending pair message (code=%s, hostname=%s)", code, hostname)
	td.send(&Message{
		Type:     "pair",
		Code:     code,
		Token:    td.token,
		Hostname: hostname,
	})
	// Don't delete the pair code here — keep retrying until
	// the relay sends pair_confirmed (which deletes it).
	// This handles the edge case where the install script saves
	// a new pair code while the daemon is already running.
	log.Printf("Pair code sent (will retry until confirmed)")
}

func (td *TargetDaemon) pairRetryLoop(stop chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			td.sendPairIfNeeded()
		case <-stop:
			return
		}
	}
}


