package main

import (
	"crypto/rand"
	"fmt"
)

type Message struct {
	Type       string `json:"type"`
	ID         string `json:"id,omitempty"`
	Target     string `json:"target,omitempty"`
	Token      string `json:"token,omitempty"`
	Name       string `json:"name,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	Stream     bool   `json:"stream,omitempty"`
	OK         *bool  `json:"ok,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	// Streaming fields
	StreamName string `json:"stream_name,omitempty"`
	Data       string `json:"data,omitempty"`
	// Pairing fields
	Code                 string `json:"code,omitempty"`
	Hostname             string `json:"hostname,omitempty"`
	RequireActivationKey bool   `json:"require_activation_key,omitempty"`
	ActivationKey        string `json:"activation_key,omitempty"`
	// File transfer fields
	SrcPath  string `json:"src_path,omitempty"`
	DstPath  string `json:"dst_path,omitempty"`
	Content  string `json:"content,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Chunked file transfer fields (large files streamed as multiple frames)
	Chunked     bool  `json:"chunked,omitempty"`
	Seq         int   `json:"seq,omitempty"`
	TotalChunks int   `json:"total_chunks,omitempty"`
	TotalSize   int64 `json:"total_size,omitempty"`
	Final       bool  `json:"final,omitempty"`
	// ChunkSizeBytes is the size of each chunk in bytes. Sent in the
	// file_transfer init so the daemon can seek to the right offset
	// for parallel-stream writes.
	ChunkSizeBytes int `json:"chunk_size_bytes,omitempty"`
	// ParallelStreams indicates how many parallel WebSocket connections
	// are being used for this transfer. When > 1, the daemon uses
	// seek-based writes instead of sequential writes.
	ParallelStreams int `json:"parallel_streams,omitempty"`
	// Binary chunk flag: when true, Data is sent as a WebSocket BinaryMessage
	// (raw bytes, no base64). The JSON envelope is sent as a preceding TextMessage
	// with BinaryChunk=true and no Data field; the next frame is binary.
	BinaryChunk bool `json:"binary_chunk,omitempty"`
	// Compressed flag: when true, the binary chunk data is gzip-compressed.
	// The daemon decompresses before writing to disk.
	Compressed bool `json:"compressed,omitempty"`
	// Stdin data for command execution (issue #6: pipe stdin to remote command)
	StdinData string `json:"stdin_data,omitempty"`
	// Multi-target fields
	Targets     []string          `json:"targets,omitempty"`
	Tokens      map[string]string `json:"tokens,omitempty"`
	Results     map[string]*Message `json:"results,omitempty"`
	Parallel    int               `json:"parallel,omitempty"`
	// Tunnel fields
	TunnelID   string `json:"tunnel_id,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
}

func streamEndOK(id string, exitCode int, durationMs int64) *Message {
	b := exitCode == 0
	return &Message{
		Type:       "stream_end",
		ID:         id,
		OK:         &b,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	}
}

func streamEndErr(id, errMsg string) *Message {
	b := false
	return &Message{
		Type:  "stream_end",
		ID:    id,
		OK:    &b,
		Error: errMsg,
	}
}

func okResult(id, stdout, stderr string, exitCode int, durationMs int64) *Message {
	b := true
	return &Message{
		Type:       "result",
		ID:         id,
		OK:         &b,
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	}
}

func errResult(id, msg string) *Message {
	b := false
	return &Message{
		Type:  "result",
		ID:    id,
		OK:    &b,
		Error: msg,
	}
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func wsURL(raw string) string {
	if len(raw) >= 7 && raw[:7] == "http://" {
		return "ws://" + raw[7:]
	}
	if len(raw) >= 8 && raw[:8] == "https://" {
		return "wss://" + raw[8:]
	}
	if len(raw) >= 5 && raw[:5] == "ws://" {
		return raw
	}
	if len(raw) >= 6 && raw[:6] == "wss://" {
		return raw
	}
	return "ws://" + raw
}
