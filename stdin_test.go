package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestIntegrationStdinForwarding verifies that StdinData is forwarded through
// the relay to the remote command's stdin (issue #6).
func TestIntegrationStdinForwarding(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "stdin-box", "stok")
	defer daemon.Close()

	// Fake daemon: executes commands with stdin forwarding
	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" && msg.StdinData != "" {
				// Decode base64 stdin data (as the real daemon does)
				stdinBytes, _ := base64.StdEncoding.DecodeString(msg.StdinData)
				daemon.WriteJSON(&Message{
					Type:   "result",
					ID:     msg.ID,
					OK:     boolPtr(true),
					Stdout: string(stdinBytes),
				})
				return
			}
			if msg.Type == "command" {
				daemon.WriteJSON(&Message{
					Type:   "result",
					ID:     msg.ID,
					OK:     boolPtr(true),
					Stdout: "no-stdin",
				})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Send a command with stdin data through the relay
	client := testClient(t, port)
	defer client.Close()

	stdinContent := "hello from piped stdin\n"
	// Client base64-encodes stdin data before sending
	encodedStdin := base64.StdEncoding.EncodeToString([]byte(stdinContent))
	client.WriteJSON(&Message{
		Type:      "execute",
		ID:        newID(),
		Target:    "stdin-box",
		Token:     "stok",
		Cmd:       "cat",
		Timeout:   5,
		StdinData: encodedStdin,
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("read result: %v", err)
	}

	if result.OK == nil || !*result.OK {
		t.Fatalf("command failed: %s", result.Error)
	}
	if result.Stdout != stdinContent {
		t.Errorf("stdout = %q, want %q", result.Stdout, stdinContent)
	}
}

// TestIntegrationStdinNoData verifies commands without StdinData work normally.
func TestIntegrationStdinNoData(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "nostdin-box", "ntok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" {
				if msg.StdinData != "" {
					daemon.WriteJSON(&Message{
						Type:   "result",
						ID:     msg.ID,
						OK:     boolPtr(false),
						Error:  "unexpected stdin data",
					})
					return
				}
				daemon.WriteJSON(&Message{
					Type:   "result",
					ID:     msg.ID,
					OK:     boolPtr(true),
					Stdout: "ok-no-stdin",
				})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type:    "execute",
		ID:      newID(),
		Target:  "nostdin-box",
		Token:   "ntok",
		Cmd:     "echo ok",
		Timeout: 5,
	})

	var result Message
	if err := client.ReadJSON(&result); err != nil {
		t.Fatalf("read result: %v", err)
	}

	if result.OK == nil || !*result.OK {
		t.Fatalf("command failed: %s", result.Error)
	}
	if result.Stdout != "ok-no-stdin" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "ok-no-stdin")
	}
}

// TestBinaryChunkDaemonReassembly tests the daemon's handleBinaryChunk
// method directly (no relay) to verify binary frame reassembly.
func TestBinaryChunkDaemonReassembly(t *testing.T) {
	tmpDir := t.TempDir()
	dstPath := filepath.Join(tmpDir, "binary-out.bin")

	// Build a TargetDaemon with reassembly state
	td := &TargetDaemon{
		name:      "test",
		token:     "tok",
		reassembly: make(map[string]*fileReassembly),
	}

	transferID := "bin-test-1"
	td.reassembly[transferID] = &fileReassembly{
		mode: "scp",
		src:  "/src",
		dst:  dstPath,
	}

	// 3000 bytes, 3 chunks of 1000
	payload := bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 1000)
	chunks := chunkData(payload, 1000)
	total := len(chunks)

	for i, chunk := range chunks {
		// Send header
		td.handleFileChunk(&Message{
			Type:        "file_chunk",
			ID:          transferID,
			Seq:         i,
			Final:       i == total-1,
			BinaryChunk: true,
		})
		// Send binary data
		td.handleBinaryChunk(chunk)
	}

	// Verify file was written
	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("file content mismatch: got %d bytes, want %d bytes", len(data), len(payload))
	}
}

// TestBinaryChunkOrphan verifies no panic on binary frame with no pending transfer.
func TestBinaryChunkOrphan(t *testing.T) {
	td := &TargetDaemon{
		name:      "test",
		token:     "tok",
		reassembly: make(map[string]*fileReassembly),
	}

	// Should not panic, just log
	td.handleBinaryChunk([]byte("orphan data"))

	// pendingBinaryChunk should be nil
	if td.pendingBinaryChunk != nil {
		t.Error("pendingBinaryChunk should be nil after orphan binary chunk")
	}
}

// TestLegacyBase64ChunkStillWorks verifies the daemon still handles
// old-style base64 chunks (backwards compatibility).
func TestLegacyBase64ChunkStillWorks(t *testing.T) {
	tmpDir := t.TempDir()
	dstPath := filepath.Join(tmpDir, "legacy-out.bin")

	td := &TargetDaemon{
		name:      "test",
		token:     "tok",
		reassembly: make(map[string]*fileReassembly),
	}

	transferID := "legacy-test-1"
	td.reassembly[transferID] = &fileReassembly{
		mode: "scp",
		src:  "/src",
		dst:  dstPath,
	}

	payload := []byte("legacy base64 chunk data")
	encoded := base64.StdEncoding.EncodeToString(payload)

	// Send legacy chunk (no BinaryChunk flag, Data has base64)
	td.handleFileChunk(&Message{
		Type:  "file_chunk",
		ID:    transferID,
		Seq:   0,
		Final: true,
		Data:  encoded,
	})

	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("file content = %q, want %q", string(data), string(payload))
	}
}

// TestRelayForwardsStdinData verifies the relay forwards StdinData in commands.
func TestRelayForwardsStdinData(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "fwd-box", "ftok")
	defer daemon.Close()

	var receivedStdin string
	done := make(chan struct{})
	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" {
				receivedStdin = msg.StdinData
				daemon.WriteJSON(&Message{
					Type:   "result",
					ID:     msg.ID,
					OK:     boolPtr(true),
					Stdout: "ok",
				})
				close(done)
				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	client.WriteJSON(&Message{
		Type:      "execute",
		ID:        newID(),
		Target:    "fwd-box",
		Token:     "ftok",
		Cmd:       "cat",
		Timeout:   5,
		StdinData: "forwarded stdin data",
	})

	select {
	case <-done:
		// The relay forwards StdinData as-is (the client base64-encodes before sending)
		if receivedStdin != "forwarded stdin data" {
			t.Errorf("relay forwarded stdin = %q, want %q", receivedStdin, "forwarded stdin data")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for daemon to receive command")
	}
}

// TestRelayBinaryChunkForwarding verifies the relay correctly forwards
// binary chunk headers + binary data frames to the target.
func TestRelayBinaryChunkForwarding(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "bchunk-box", "btok")
	defer daemon.Close()

	// Fake daemon: reassembles binary chunks
	got := make(chan int, 1)
	go func() {
		buffers := make(map[string]*bytes.Buffer)
		var pendingID string
		var pendingFinal bool
		for {
			msgType, rawData, err := daemon.ReadMessage()
			if err != nil {
				return
			}
			if msgType == websocket.BinaryMessage {
				if pendingID != "" {
					buf, ok := buffers[pendingID]
					if ok {
						buf.Write(rawData)
						if pendingFinal {
							got <- buf.Len()
							daemon.WriteJSON(&Message{
								Type: "file_transfer_result",
								ID:   pendingID,
								OK:   boolPtr(true),
							})
						}
					}
					pendingID = ""
				}
				continue
			}
			var msg Message
			if err := json.Unmarshal(rawData, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "file_transfer":
				if msg.Chunked {
					buffers[msg.ID] = &bytes.Buffer{}
				}
			case "file_chunk":
				if msg.BinaryChunk {
					pendingID = msg.ID
					pendingFinal = msg.Final
				} else {
					// Legacy
					buf, ok := buffers[msg.ID]
					if !ok {
						continue
					}
					buf.Write([]byte(msg.Data))
					if msg.Final {
						got <- buf.Len()
						daemon.WriteJSON(&Message{
							Type: "file_transfer_result",
							ID:   msg.ID,
							OK:   boolPtr(true),
						})
					}
				}
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	client := testClient(t, port)
	defer client.Close()

	// Send a chunked binary transfer through the relay
	payload := bytes.Repeat([]byte("B"), 5000)
	base := &Message{
		ID:      newID(),
		Target:  "bchunk-box",
		Token:   "btok",
		Mode:    "scp",
		SrcPath: "/src",
		DstPath: "/dst",
	}
	if err := sendFileFramesWithSize(client, base, payload, false, 2000); err != nil {
		t.Fatalf("sendFileFrames: %v", err)
	}

	select {
	case n := <-got:
		if n != len(payload) {
			t.Errorf("daemon received %d bytes, want %d", n, len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for binary chunk transfer")
	}
}
