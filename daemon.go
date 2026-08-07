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

	reMu       sync.Mutex
	reassembly map[string]*fileReassembly
}

// fileReassembly accumulates the chunks of an in-flight chunked file transfer
// until the final frame arrives, at which point the payload is written out.
type fileReassembly struct {
	mode string
	src  string
	dst  string
	buf  bytes.Buffer
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

// beginChunkedTransfer registers reassembly state for an incoming chunked
// file transfer. The actual bytes arrive as subsequent "file_chunk" frames.
func (td *TargetDaemon) beginChunkedTransfer(msg *Message) {
	td.reMu.Lock()
	defer td.reMu.Unlock()
	if td.reassembly == nil {
		td.reassembly = make(map[string]*fileReassembly)
	}
	td.reassembly[msg.ID] = &fileReassembly{
		mode: msg.Mode,
		src:  msg.SrcPath,
		dst:  msg.DstPath,
	}
}

// handleFileChunk appends a chunk to its transfer's buffer and, on the final
// chunk, writes the reassembled payload to disk.
func (td *TargetDaemon) handleFileChunk(msg *Message) {
	td.reMu.Lock()
	r, ok := td.reassembly[msg.ID]
	td.reMu.Unlock()
	if !ok {
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "received chunk for unknown transfer"})
		return
	}

	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		td.dropReassembly(msg.ID)
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to decode chunk: " + err.Error()})
		return
	}
	r.buf.Write(data)

	if !msg.Final {
		return
	}

	td.dropReassembly(msg.ID)
	if err := writeTransfer(r); err != nil {
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: err.Error()})
		return
	}
	log.Printf("Chunked file transfer succeeded (id=%s): %s -> %s (%d bytes)", msg.ID, r.src, r.dst, r.buf.Len())
	td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(true)})
}

func (td *TargetDaemon) dropReassembly(id string) {
	td.reMu.Lock()
	delete(td.reassembly, id)
	td.reMu.Unlock()
}

// writeTransfer persists a fully reassembled transfer to disk, honoring the
// same scp (single file) and rsync (tar archive) modes as the buffered path.
func writeTransfer(r *fileReassembly) error {
	switch r.mode {
	case "scp":
		if err := os.WriteFile(r.dst, r.buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("failed to write file: %v", err)
		}
		return nil
	case "rsync":
		if err := os.MkdirAll(r.dst, 0755); err != nil {
			return fmt.Errorf("failed to create destination directory: %v", err)
		}
		if err := extractTarArchive(r.buf.Bytes(), r.dst); err != nil {
			return fmt.Errorf("failed to extract tar archive: %v", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown file transfer mode: %s", r.mode)
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


