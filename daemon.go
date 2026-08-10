package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
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
	// pendingBinaryChunk tracks the chunk header that precedes a binary frame
	pendingBinaryChunk *fileReassembly
}

// fileReassembly accumulates the chunks of an in-flight chunked file transfer
// until the final frame arrives, at which point the payload is written out.
// For scp mode, chunks are streamed directly to disk via fileWriter to avoid
// buffering the entire file in memory.
type fileReassembly struct {
	mode           string
	src            string
	dst            string
	buf            bytes.Buffer // used for rsync (tar) mode
	id             string
	seq            int
	final          bool
	fileWriter     *os.File // used for scp mode — stream to disk
	compressed     bool     // current binary chunk is gzip-compressed
	chunkSizeBytes int      // size of each chunk for seek-based writes
	parallel       bool     // parallel stream mode — use seek writes
	chunksReceived int      // count of chunks written (for parallel completion)
	totalChunks    int      // total expected chunks (for parallel completion)
	writeMu        sync.Mutex // protects fileWriter seeks for parallel writes
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
	conn, _, err := wsDialer().Dial(td.relayURL, nil)
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
// For scp mode, a file writer is opened immediately so chunks stream to disk.
func (td *TargetDaemon) beginChunkedTransfer(msg *Message) {
	td.reMu.Lock()
	defer td.reMu.Unlock()
	if td.reassembly == nil {
		td.reassembly = make(map[string]*fileReassembly)
	}
	r := &fileReassembly{
		mode:           msg.Mode,
		src:            msg.SrcPath,
		dst:            msg.DstPath,
		chunkSizeBytes: msg.ChunkSizeBytes,
		totalChunks:    msg.TotalChunks,
	}
	// Parallel stream mode: multiple connections send chunks for the same file.
	// Use a shared file writer with seek-based writes, keyed by dst path.
	if msg.ParallelStreams > 1 && msg.Mode == "scp" {
		r.parallel = true
		// Check if we already opened the file for a previous parallel stream
		// with the same dst path.
		for _, existing := range td.reassembly {
			if existing.dst == msg.DstPath && existing.fileWriter != nil && existing.parallel {
				r.fileWriter = existing.fileWriter
				break
			}
		}
		if r.fileWriter == nil {
			// First stream: create and pre-allocate the file
			f, err := os.Create(msg.DstPath)
			if err != nil {
				log.Printf("Failed to create destination file for parallel streaming: %v", err)
			} else {
				r.fileWriter = f
				if msg.TotalSize > 0 {
					f.Truncate(msg.TotalSize)
				}
			}
		}
		td.reassembly[msg.ID] = r
		return
	}
	// For scp mode, open the destination file immediately and stream to disk
	if msg.Mode == "scp" {
		f, err := os.Create(msg.DstPath)
		if err != nil {
			log.Printf("Failed to create destination file for streaming: %v", err)
			// Fall back to buffer mode
		} else {
			r.fileWriter = f
		}
	}
	td.reassembly[msg.ID] = r
}

// handleFileChunk appends a chunk to its transfer's buffer and, on the final
// chunk, writes the reassembled payload to disk. Supports both legacy base64
// chunks (Data field) and binary chunks (BinaryChunk=true, data in next frame).
func (td *TargetDaemon) handleFileChunk(msg *Message) {
	td.reMu.Lock()
	r, ok := td.reassembly[msg.ID]
	td.reMu.Unlock()
	if !ok {
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "received chunk for unknown transfer"})
		return
	}

	if msg.BinaryChunk {
		// Binary chunk: data arrives in the next WebSocket binary frame.
		r.id = msg.ID
		r.seq = msg.Seq
		r.final = msg.Final
		r.compressed = msg.Compressed
		td.pendingBinaryChunk = r
		return
	}

	// Legacy base64 chunk
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		td.dropReassembly(msg.ID)
		td.closeFileWriter(r)
		td.send(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(false), Error: "failed to decode chunk: " + err.Error()})
		return
	}
	td.writeChunkData(r, data)

	if !msg.Final {
		return
	}

	td.finishReassembly(r)
}

// handleBinaryChunk appends raw binary data to the pending transfer and
// finalizes if the pending chunk was marked as final. Decompresses gzip
// data if the chunk header had Compressed=true.
func (td *TargetDaemon) handleBinaryChunk(data []byte) {
	td.reMu.Lock()
	r := td.pendingBinaryChunk
	td.pendingBinaryChunk = nil
	td.reMu.Unlock()
	if r == nil {
		log.Printf("Received binary chunk with no pending transfer")
		return
	}
	// Decompress if needed
	if r.compressed {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			log.Printf("Failed to create gzip reader for chunk: %v", err)
			td.dropReassembly(r.id)
			td.closeFileWriter(r)
			td.send(&Message{Type: "file_transfer_result", ID: r.id, OK: boolPtr(false), Error: "gzip decompress failed: " + err.Error()})
			return
		}
		decompressed, err := io.ReadAll(gz)
		gz.Close()
		if err != nil {
			log.Printf("Failed to decompress chunk: %v", err)
			td.dropReassembly(r.id)
			td.closeFileWriter(r)
			td.send(&Message{Type: "file_transfer_result", ID: r.id, OK: boolPtr(false), Error: "gzip read failed: " + err.Error()})
			return
		}
		data = decompressed
		r.compressed = false
	}
	td.writeChunkData(r, data)
	if r.final {
		td.finishReassembly(r)
	}
}

// writeChunkData writes chunk data to the file writer (scp streaming) or
// to the in-memory buffer (rsync/tar mode, or fallback if file open failed).
// For parallel streams, seeks to seq*chunkSize before writing.
func (td *TargetDaemon) writeChunkData(r *fileReassembly, data []byte) {
	if r.fileWriter != nil {
		if r.parallel && r.chunkSizeBytes > 0 {
			// Parallel mode: seek to the right offset for this chunk
			offset := int64(r.seq) * int64(r.chunkSizeBytes)
			r.writeMu.Lock()
			if _, err := r.fileWriter.Seek(offset, 0); err != nil {
				log.Printf("Failed to seek to offset %d: %v", offset, err)
				r.writeMu.Unlock()
				r.buf.Write(data)
				return
			}
			if _, err := r.fileWriter.Write(data); err != nil {
				log.Printf("Failed to write chunk at offset %d: %v", offset, err)
				r.writeMu.Unlock()
				r.buf.Write(data)
				return
			}
			r.writeMu.Unlock()
		} else {
			// Sequential mode: just append
			if _, err := r.fileWriter.Write(data); err != nil {
				log.Printf("Failed to write chunk to file: %v", err)
				// Fall back to buffer
				r.buf.Write(data)
			}
		}
	} else {
		r.buf.Write(data)
	}
}

// closeFileWriter closes the streaming file writer if open.
func (td *TargetDaemon) closeFileWriter(r *fileReassembly) {
	if r.fileWriter != nil {
		r.fileWriter.Close()
		r.fileWriter = nil
	}
}

// finishReassembly writes the reassembled payload to disk and sends the result.
func (td *TargetDaemon) finishReassembly(r *fileReassembly) {
	td.dropReassembly(r.id)

	var totalBytes int64
	if r.parallel {
		// Parallel mode: don't close the file here — it's shared across streams.
		// Just send the result for this stream. The file was pre-allocated.
		// The file will be closed when the daemon process exits or when
		// a subsequent non-parallel transfer reuses the dst path.
		log.Printf("Parallel stream completed (id=%s): %s -> %s", r.id, r.src, r.dst)
		td.send(&Message{Type: "file_transfer_result", ID: r.id, OK: boolPtr(true)})
		return
	}
	if r.fileWriter != nil {
		// scp streaming mode — file is already written, just close
		stat, _ := r.fileWriter.Stat()
		if stat != nil {
			totalBytes = stat.Size()
		}
		r.fileWriter.Close()
		r.fileWriter = nil
	} else {
		// Buffer mode — write to disk now
		if err := writeTransfer(r); err != nil {
			td.send(&Message{Type: "file_transfer_result", ID: r.id, OK: boolPtr(false), Error: err.Error()})
			return
		}
		totalBytes = int64(r.buf.Len())
	}

	log.Printf("Chunked file transfer succeeded (id=%s): %s -> %s (%d bytes)", r.id, r.src, r.dst, totalBytes)
	td.send(&Message{Type: "file_transfer_result", ID: r.id, OK: boolPtr(true)})
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


