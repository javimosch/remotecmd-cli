package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// fileChunkSize is the raw size of each file transfer chunk.
	// 2 MiB — benchmarked optimal on high-RTT relay links (77ms RTT
	// through dk1). Smaller chunks allow better pipelining: the relay's
	// async write queue can forward chunk N to the target while the
	// client sends chunk N+1. With 8 MiB chunks, only 2 chunks fit in a
	// 10 MiB transfer, limiting overlap. 2 MiB gives 5 chunks for 10 MiB,
	// keeping the pipeline full.
	fileChunkSize = 2 << 20 // 2 MiB

	// relayMaxFrameSize is the maximum WebSocket frame the relay will accept.
	relayMaxFrameSize = 32 << 20 // 32 MiB

	// compressionThreshold is the ratio below which we use the compressed version.
	compressionThreshold = 0.9

	// compressionSampleSize is the number of bytes to test-compress as a
	// quick heuristic. If the sample doesn't compress well, skip the full
	// chunk — avoids wasting CPU on incompressible data (random binary,
	// already-compressed files, encrypted data).
	compressionSampleSize = 4096
)

// effectiveChunkSize returns the chunk size, optionally overridden by the
// RCMD_CHUNK_SIZE environment variable (in bytes) for benchmarking.
func effectiveChunkSize() int {
	if s := os.Getenv("RCMD_CHUNK_SIZE"); s != "" {
		var n int
		fmt.Sscanf(s, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return fileChunkSize
}

// compressPool reuses gzip writer buffers across chunks to reduce GC pressure.
var compressPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// gzipWriterPool reuses gzip.Writer objects across chunks.
// gzip.Writer is ~40KB, so avoiding allocation per chunk is important.
var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		gz, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return gz
	},
}

// tryCompress gzip-compresses data and returns the compressed version if it's
// smaller than the original by at least 10%. Uses a sample-based heuristic to
// skip incompressible data quickly without compressing the full chunk.
func tryCompress(data []byte) []byte {
	if len(data) < 1024 {
		return nil // too small to bother
	}

	// Quick heuristic: compress a small sample first. If it doesn't compress
	// well, the full chunk almost certainly won't either — skip it.
	sampleEnd := compressionSampleSize
	if sampleEnd > len(data) {
		sampleEnd = len(data)
	}
	sample := data[:sampleEnd]
	var sampleBuf bytes.Buffer
	sgz, _ := gzip.NewWriterLevel(&sampleBuf, gzip.BestSpeed)
	sgz.Write(sample)
	sgz.Close()
	// If the sample compressed to >= 95% of original, the data is likely
	// incompressible — don't waste time compressing the full chunk.
	if float64(sampleBuf.Len()) >= float64(len(sample))*0.95 {
		return nil
	}

	// Sample compressed well — compress the full chunk.
	buf := compressPool.Get().(*bytes.Buffer)
	buf.Reset()
	gz := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(buf)
	if _, err := gz.Write(data); err != nil {
		gz.Close()
		gzipWriterPool.Put(gz)
		compressPool.Put(buf)
		return nil
	}
	if err := gz.Close(); err != nil {
		gz.Reset(nil) // reset for reuse
		gzipWriterPool.Put(gz)
		compressPool.Put(buf)
		return nil
	}
	// Put gzip writer back in pool for reuse (after Close, it's safe to reset)
	gz.Reset(nil)
	gzipWriterPool.Put(gz)

	if float64(buf.Len()) < float64(len(data))*compressionThreshold {
		result := make([]byte, buf.Len())
		copy(result, buf.Bytes())
		compressPool.Put(buf)
		return result
	}
	compressPool.Put(buf)
	return nil
}

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

	if stream {
		// Emit start event
		emitProgress("start", map[string]interface{}{
			"src":  src,
			"dst":  dst,
			"size": info.Size(),
			"type": mapType(info),
		})
	}

	// For directories, we still need to create the tar archive in memory.
	// For single files, we stream directly from disk to avoid loading
	// the entire file into memory.
	var tarData []byte
	var fileReader *os.File
	if info.IsDir() {
		mode = "rsync"
		tarData, err = createTarArchive(src)
		if err != nil {
			return fmt.Errorf("creating tar archive: %v", err)
		}
		if stream {
			emitProgress("archived", map[string]interface{}{
				"size": len(tarData),
			})
		}
	} else {
		mode = "scp"
		fileReader, err = os.Open(src)
		if err != nil {
			return fmt.Errorf("opening source file: %v", err)
		}
		defer fileReader.Close()
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := dialRelay(u)
	if err != nil {
		return fmt.Errorf("connecting to relay: %v", err)
	}
	defer conn.Close()
	// Set generous write deadline so large chunks don't timeout on slow links
	conn.SetWriteDeadline(time.Now().Add(10 * time.Minute))

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
		Mode:    mode,
		SrcPath: src,
		DstPath: dst,
	}

	// Send the payload — streaming from disk for single files, from memory for tar
	chunkSz := effectiveChunkSize()

	// Parallel streams: auto-enable for files over scp mode.
	// Override with RCMD_PARALLEL_STREAMS env var (0 = disabled, N = N streams).
	// Auto-tuning based on file size (CERN/GridFTP research: 2-10 streams
	// optimal, linear gain up to ~10 streams, diminishing returns beyond):
	//   5-20 MiB  → 2 streams
	//   20-100 MiB → 3 streams
	//   100+ MiB  → 4 streams
	parallelStreams := 0
	if s := os.Getenv("RCMD_PARALLEL_STREAMS"); s != "" {
		fmt.Sscanf(s, "%d", &parallelStreams)
	} else if fileReader != nil && mode == "scp" {
		size := info.Size()
		switch {
		case size > 100*1024*1024:
			parallelStreams = 4
		case size > 20*1024*1024:
			parallelStreams = 3
		case size > 5*1024*1024:
			parallelStreams = 2
		}
	}
	if parallelStreams > 1 && fileReader != nil && mode == "scp" {
		conn.Close()
		return sendFileParallel(cfg, relayTarget, tgt.Token, src, dst, info.Size(), stream, chunkSz, parallelStreams)
	}

	var totalSent int64
	var totalChunks int
	if fileReader != nil {
		totalSent, totalChunks, err = sendFileStreaming(conn, base, fileReader, info.Size(), stream, chunkSz)
	} else {
		totalChunks = chunkCount(len(tarData), chunkSz)
		err = sendFileFramesWithSize(conn, base, tarData, stream, chunkSz)
		totalSent = int64(len(tarData))
	}
	if err != nil {
		if stream {
			emitProgress("error", map[string]interface{}{
				"message": err.Error(),
			})
		}
		return err
	}

	if stream {
		emitProgress("sent", map[string]interface{}{
			"total_bytes": totalSent,
			"chunks":      totalChunks,
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

// sendFileParallel opens N WebSocket connections to the relay and sends
// chunks concurrently. Each stream sends a subset of chunks with the same
// transfer ID. The daemon uses seek-based writes to place each chunk at
// the correct offset. This saturates the link better on high-RTT paths
// where a single TCP stream can't fill the pipe.
func sendFileParallel(cfg *Config, target, token, src, dst string, totalSize int64, stream bool, chunkSz, numStreams int) error {
	u := wsURL(cfg.Relay.URL)
	totalChunks := chunkCount(int(totalSize), chunkSz)

	// Assign chunks to streams: stream i gets chunks [i, i+numStreams, i+2*numStreams, ...]
	// Each stream sends its own file_transfer init with ParallelStreams=numStreams.
	// All streams use the same transfer ID so the daemon shares the file writer.

	transferID := newID()

	if stream {
		emitProgress("start", map[string]interface{}{
			"src":              src,
			"dst":              dst,
			"size":             totalSize,
			"type":             "file",
			"parallel_streams": numStreams,
		})
	}

	type streamResult struct {
		err error
	}

	resultCh := make(chan streamResult, numStreams)

	for s := 0; s < numStreams; s++ {
		go func(streamIdx int) {
			conn, _, err := dialRelay(u)
			if err != nil {
				resultCh <- streamResult{err: fmt.Errorf("stream %d connect: %v", streamIdx, err)}
				return
			}
			defer conn.Close()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Minute))

			// Send file_transfer init for this stream
			init := &Message{
				Type:           "file_transfer",
				ID:             transferID,
				Target:         target,
				Token:          token,
				Mode:           "scp",
				SrcPath:        src,
				DstPath:        dst,
				Chunked:        true,
				TotalChunks:    totalChunks,
				TotalSize:      totalSize,
				ChunkSizeBytes: chunkSz,
				ParallelStreams: numStreams,
			}
			frame, err := marshalWithinLimit(init, relayMaxFrameSize)
			if err != nil {
				resultCh <- streamResult{err: err}
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				resultCh <- streamResult{err: fmt.Errorf("stream %d init: %v", streamIdx, err)}
				return
			}

			// Open the file and seek to this stream's first chunk
			f, err := os.Open(src)
			if err != nil {
				resultCh <- streamResult{err: fmt.Errorf("stream %d open: %v", streamIdx, err)}
				return
			}
			defer f.Close()

			buf := make([]byte, chunkSz)
			var chunksSent int
			for seq := streamIdx; seq < totalChunks; seq += numStreams {
				offset := int64(seq) * int64(chunkSz)
				if _, err := f.Seek(offset, 0); err != nil {
					resultCh <- streamResult{err: fmt.Errorf("stream %d seek: %v", streamIdx, err)}
					return
				}
				n, readErr := io.ReadFull(f, buf)
				if n == 0 && readErr != nil {
					resultCh <- streamResult{err: fmt.Errorf("stream %d read: %v", streamIdx, readErr)}
					return
				}

				isFinal := seq+numStreams >= totalChunks
				payload := buf[:n]
				compressed := tryCompress(payload)
				isCompressed := compressed != nil
				if isCompressed {
					payload = compressed
				}

				header := &Message{
					Type:        "file_chunk",
					ID:          transferID,
					Seq:         seq,
					Final:       isFinal,
					BinaryChunk: true,
					Compressed:  isCompressed,
				}
				headerFrame, err := marshalWithinLimit(header, relayMaxFrameSize)
				if err != nil {
					resultCh <- streamResult{err: err}
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, headerFrame); err != nil {
					resultCh <- streamResult{err: fmt.Errorf("stream %d header: %v", streamIdx, err)}
					return
				}
				if len(payload) > 0 {
					if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
						resultCh <- streamResult{err: fmt.Errorf("stream %d data: %v", streamIdx, err)}
						return
					}
				}
				chunksSent++
			}

			// Wait for this stream's result
			for {
				var response Message
				if err := conn.ReadJSON(&response); err != nil {
					resultCh <- streamResult{err: fmt.Errorf("stream %d read result: %v", streamIdx, err)}
					return
				}
				if response.Type == "result" && response.ID == transferID {
					if response.OK != nil && !*response.OK {
						resultCh <- streamResult{err: fmt.Errorf("stream %d failed: %s", streamIdx, response.Error)}
						return
					}
					resultCh <- streamResult{err: nil}
					return
				}
			}
		}(s)
	}

	// Wait for all streams
	var firstErr error
	for i := 0; i < numStreams; i++ {
		res := <-resultCh
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
	}

	if stream {
		if firstErr != nil {
			emitProgress("error", map[string]interface{}{"message": firstErr.Error()})
		} else {
			emitProgress("sent", map[string]interface{}{
				"total_bytes":      totalSize,
				"chunks":           totalChunks,
				"parallel_streams": numStreams,
			})
			emitProgress("complete", map[string]interface{}{"ok": true})
		}
	}

	return firstErr
}

// sendFileStreaming reads a file in chunks and sends each chunk as it's read,
// avoiding loading the entire file into memory. This overlaps disk I/O with
// network writes for better throughput on large files.
func sendFileStreaming(conn frameWriter, base *Message, r io.Reader, totalSize int64, stream bool, chunkSize int) (int64, int, error) {
	totalChunks := chunkCount(int(totalSize), chunkSize)

	// Send init frame
	init := &Message{
		Type:        "file_transfer",
		ID:          base.ID,
		Target:      base.Target,
		Token:       base.Token,
		Mode:        base.Mode,
		SrcPath:     base.SrcPath,
		DstPath:     base.DstPath,
		Chunked:     true,
		TotalChunks: totalChunks,
		TotalSize:   totalSize,
	}
	frame, err := marshalWithinLimit(init, relayMaxFrameSize)
	if err != nil {
		return 0, 0, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		return 0, 0, fmt.Errorf("sending file_transfer init: %v", err)
	}

	buf := make([]byte, chunkSize)
	var sent int64
	for i := 0; ; i++ {
		n, readErr := io.ReadFull(r, buf)
		if n == 0 && readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				// Final chunk was already sent (or file is empty)
				if i == 0 {
					// Empty file — send one empty final chunk
					header := &Message{
						Type:        "file_chunk",
						ID:          base.ID,
						Seq:         0,
						Final:       true,
						BinaryChunk: true,
					}
					hf, _ := marshalWithinLimit(header, relayMaxFrameSize)
					if err := conn.WriteMessage(websocket.TextMessage, hf); err != nil {
						return 0, 0, fmt.Errorf("sending empty chunk header: %v", err)
					}
				}
				break
			}
			return sent, i, fmt.Errorf("reading file: %v", readErr)
		}

		isFinal := readErr == io.ErrUnexpectedEOF || (readErr == nil && sent+int64(n) >= totalSize)
		if readErr == nil && sent+int64(n) < totalSize {
			isFinal = false
		}

		// Try adaptive compression — use compressed version only if it saves 10%+
		payload := buf[:n]
		compressed := tryCompress(payload)
		isCompressed := compressed != nil
		if isCompressed {
			payload = compressed
		}

		// Send JSON header
		header := &Message{
			Type:        "file_chunk",
			ID:          base.ID,
			Seq:         i,
			Final:       isFinal,
			BinaryChunk: true,
			Compressed:  isCompressed,
		}
		headerFrame, err := marshalWithinLimit(header, relayMaxFrameSize)
		if err != nil {
			return sent, i, err
		}
		if err := conn.WriteMessage(websocket.TextMessage, headerFrame); err != nil {
			return sent, i, fmt.Errorf("sending file chunk header %d: %v", i, err)
		}
		// Send binary payload (compressed or raw)
		if len(payload) > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				return sent, i, fmt.Errorf("sending file chunk data %d: %v", i, err)
			}
		}
		sent += int64(n)
		if stream {
			emitProgress("chunk", map[string]interface{}{
				"seq":         i,
				"chunks":      totalChunks,
				"chunk_bytes": n,
				"sent_bytes":  sent,
				"total_bytes": totalSize,
				"compressed":  isCompressed,
				"wire_bytes":  len(payload),
			})
		}
		if isFinal {
			break
		}
	}
	return sent, totalChunks, nil
}

// sendFileFrames streams data to the relay as an initial "file_transfer"
// announcement followed by one or more "file_chunk" frames. Each chunk is
// sent as a small JSON header (TextMessage) + a raw binary frame (BinaryMessage),
// eliminating base64 overhead and JSON re-serialization on the relay.
func sendFileFrames(conn frameWriter, base *Message, data []byte, stream bool) error {
	return sendFileFramesWithSize(conn, base, data, stream, effectiveChunkSize())
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
		// Try adaptive compression
		payload := chunk
		compressed := tryCompress(chunk)
		isCompressed := compressed != nil
		if isCompressed {
			payload = compressed
		}

		// Send JSON header (tiny — no Data field, no base64)
		header := &Message{
			Type:        "file_chunk",
			ID:          base.ID,
			Seq:         i,
			Final:       i == total-1,
			BinaryChunk: true,
			Compressed:  isCompressed,
		}
		headerFrame, err := marshalWithinLimit(header, relayMaxFrameSize)
		if err != nil {
			return err
		}
		if err := conn.WriteMessage(websocket.TextMessage, headerFrame); err != nil {
			return fmt.Errorf("sending file chunk header %d/%d: %v", i+1, total, err)
		}
		// Send binary payload (compressed or raw)
		if len(payload) > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
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
				"compressed":  isCompressed,
				"wire_bytes":  len(payload),
			})
		}
	}
	return nil
}
