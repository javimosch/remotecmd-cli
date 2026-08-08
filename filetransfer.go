package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// fileChunkSize is the raw size of each file transfer chunk.
	// 8 MiB raw — sent as a WebSocket BinaryMessage (no base64 overhead).
	// The JSON envelope for each chunk is tiny (~100 bytes), so the relay
	// frame limit only applies to the binary frame which carries raw bytes.
	fileChunkSize = 8 << 20 // 8 MiB

	// relayMaxFrameSize is the maximum WebSocket frame the relay will accept.
	// Frames larger than this are rejected by the relay, so the client caps
	// each chunk well below the limit and surfaces a clear error otherwise.
	relayMaxFrameSize = 16 << 20 // 16 MiB (increased from 12 to fit 8 MiB binary chunks)
)

func handleCP(args []string) {
	fs := flag.NewFlagSet("cp", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	src := fs.String("src", "", "source path")
	dst := fs.String("dst", "", "destination path")
	stream := fs.Bool("stream", false, "stream progress as JSONL")
	fs.Parse(args)

	if *target == "" || *src == "" || *dst == "" {
		fmt.Fprintln(os.Stderr, "Error: --target, --src, and --dst are required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli cp --target <name> --src <path> --dst <path> [--stream]")
		osExit(ExitConfigError)
	}

	if err := handleFileTransfer(*target, *src, *dst, *stream); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	if !*stream {
		fmt.Printf("Copy completed successfully\n")
	}
}

func handleFileTransfer(target, src, dst string, stream bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %v", err)
	}
	if cfg.Relay.URL == "" {
		return fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	tgt, ok := cfg.Targets[target]
	if !ok {
		return fmt.Errorf("unknown target %q. Run: remotecmd-cli add-target --name %s --token <token>", target, target)
	}

	// Auto-detect if source is directory or file
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %v", err)
	}

	var mode string
	var data []byte

	if stream {
		// Emit start event
		emitProgress("start", map[string]interface{}{
			"src":  src,
			"dst":  dst,
			"size": info.Size(),
			"type": mapType(info),
		})
	}

	if info.IsDir() {
		// Directory: use rsync mode with tar archive
		mode = "rsync"
		tarData, err := createTarArchive(src)
		if err != nil {
			return fmt.Errorf("creating tar archive: %v", err)
		}
		data = tarData
		if stream {
			emitProgress("archived", map[string]interface{}{
				"size": len(tarData),
			})
		}
	} else {
		// Single file: use scp mode
		mode = "scp"
		fileData, err := ioutil.ReadFile(src)
		if err != nil {
			return fmt.Errorf("reading source file: %v", err)
		}
		data = fileData
		if stream {
			emitProgress("read", map[string]interface{}{
				"size": len(data),
			})
		}
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("connecting to relay: %v", err)
	}
	defer conn.Close()

	// Resolve relay-registered name (may differ from local alias)
	relayTarget := target
	if tgt.RelayName != "" {
		relayTarget = tgt.RelayName
	}

	id := newID()
	base := &Message{
		ID:      id,
		Target:  relayTarget,
		Token:   tgt.Token,
		Mode:    mode, // May have been changed from rsync to scp for single files
		SrcPath: src,
		DstPath: dst,
	}

	// Stream the payload as bounded chunks so large files never exceed the
	// relay's max frame size (the whole file used to be sent as one frame,
	// which stalled silently past the limit).
	if err := sendFileFrames(conn, base, data, stream); err != nil {
		if stream {
			emitProgress("error", map[string]interface{}{
				"message": err.Error(),
			})
		}
		return err
	}

	if stream {
		emitProgress("sent", map[string]interface{}{
			"total_bytes": len(data),
			"chunks":      chunkCount(len(data), fileChunkSize),
		})
	}

	// Wait for response
	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var response Message
			if err := conn.ReadJSON(&response); err != nil {
				if stream {
					emitProgress("error", map[string]interface{}{
						"message": err.Error(),
					})
				}
				errCh <- err
				return
			}
			if response.Type == "result" && response.ID == id {
				if stream {
					emitProgress("complete", map[string]interface{}{
						"ok": response.OK,
					})
				}
				resultCh <- &response
				return
			}
		}
	}()

	select {
	case response := <-resultCh:
		if !*response.OK {
			return fmt.Errorf("file transfer failed: %s", response.Error)
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("connection error: %v", err)
	case <-time.After(10 * time.Minute):
		if stream {
			emitProgress("timeout", map[string]interface{}{})
		}
		return fmt.Errorf("timeout waiting for file transfer result")
	}
}

func mapType(info os.FileInfo) string {
	if info.IsDir() {
		return "directory"
	}
	return "file"
}

func createTarArchive(srcPath string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(srcPath, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}

		// Adjust header name to be relative to source
		relPath, err := filepath.Rel(srcPath, file)
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// If not a directory, write file content
		if !fi.IsDir() {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// chunkCount returns the number of chunks a payload of size bytes is split
// into for a given chunk size. An empty payload still yields one (empty)
// chunk so the receiver always sees a terminating "final" frame.
func chunkCount(size, chunkSize int) int {
	if size <= 0 {
		return 1
	}
	return (size + chunkSize - 1) / chunkSize
}

// chunkData splits data into slices of at most chunkSize bytes. An empty
// input yields a single empty chunk so that a terminating frame is always
// emitted (see chunkCount).
func chunkData(data []byte, chunkSize int) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var chunks [][]byte
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	return chunks
}

// marshalWithinLimit marshals msg to JSON and fails with a clear error if the
// resulting frame would exceed the relay's frame limit. This turns the old
// silent stall on oversized payloads into an actionable error.
func marshalWithinLimit(msg *Message, limit int) ([]byte, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(b) > limit {
		return nil, fmt.Errorf("message frame of %d bytes exceeds relay frame limit of %d bytes (reduce chunk size)", len(b), limit)
	}
	return b, nil
}

// frameWriter is the subset of *websocket.Conn used to send frames, so the
// transport can be exercised in tests without a live connection.
type frameWriter interface {
	WriteMessage(messageType int, data []byte) error
}

// sendFileFrames streams data to the relay as an initial "file_transfer"
// announcement followed by one or more "file_chunk" frames. Each chunk is
// sent as a small JSON header (TextMessage) + a raw binary frame (BinaryMessage),
// eliminating base64 overhead and JSON re-serialization on the relay.
func sendFileFrames(conn frameWriter, base *Message, data []byte, stream bool) error {
	return sendFileFramesWithSize(conn, base, data, stream, fileChunkSize)
}

// sendFileFramesWithSize is sendFileFrames with an explicit chunk size, used
// so tests can force multiple chunks from small payloads.
func sendFileFramesWithSize(conn frameWriter, base *Message, data []byte, stream bool, chunkSize int) error {
	chunks := chunkData(data, chunkSize)
	total := len(chunks)

	init := &Message{
		Type:        "file_transfer",
		ID:          base.ID,
		Target:      base.Target,
		Token:       base.Token,
		Mode:        base.Mode,
		SrcPath:     base.SrcPath,
		DstPath:     base.DstPath,
		Chunked:     true,
		TotalChunks: total,
		TotalSize:   int64(len(data)),
	}
	frame, err := marshalWithinLimit(init, relayMaxFrameSize)
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		return fmt.Errorf("sending file_transfer init: %v", err)
	}

	sent := 0
	for i, chunk := range chunks {
		// Send JSON header (tiny — no Data field, no base64)
		header := &Message{
			Type:        "file_chunk",
			ID:          base.ID,
			Seq:         i,
			Final:       i == total-1,
			BinaryChunk: true,
		}
		headerFrame, err := marshalWithinLimit(header, relayMaxFrameSize)
		if err != nil {
			return err
		}
		if err := conn.WriteMessage(websocket.TextMessage, headerFrame); err != nil {
			return fmt.Errorf("sending file chunk header %d/%d: %v", i+1, total, err)
		}
		// Send raw binary payload (no base64, no JSON wrapping)
		if len(chunk) > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				return fmt.Errorf("sending file chunk data %d/%d: %v", i+1, total, err)
			}
		}
		sent += len(chunk)
		if stream {
			emitProgress("chunk", map[string]interface{}{
				"seq":         i,
				"chunks":      total,
				"chunk_bytes": len(chunk),
				"sent_bytes":  sent,
				"total_bytes": len(data),
			})
		}
	}
	return nil
}
