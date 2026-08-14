package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

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
