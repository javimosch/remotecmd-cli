package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationClientHandleExec(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Start daemon
	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "exec-target", "exec-tok")
	defer daemon.Close()
	testDaemonResponder(t, daemon, 0, "exec-output")

	time.Sleep(50 * time.Millisecond)

	// Set up config with relay pointing to test server
	tmpDir, err := os.MkdirTemp("", "remotecmd-client-exec-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Configure relay and target
	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("exec-target", "exec-tok")

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = handleExec("exec-target", "echo hello", 5, false)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("handleExec: %v", err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "exec-output") {
		t.Errorf("output should contain daemon response: %s", output)
	}
	if !strings.Contains(output, `"ok": true`) {
		t.Errorf("output should indicate success: %s", output)
	}
}

func TestIntegrationClientHandleExecStreaming(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "stream-target", "stok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "command" && msg.Stream {
				daemon.WriteJSON(&Message{Type: "stream_chunk", ID: msg.ID, StreamName: "stdout", Data: "chunk-data\n"})
				daemon.WriteJSON(&Message{Type: "stream_end", ID: msg.ID, OK: boolPtr(true), ExitCode: 0, DurationMs: 5})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	tmpDir, err := os.MkdirTemp("", "remotecmd-client-stream-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("stream-target", "stok")

	// handleExec with streaming returns JSONL events, then a final JSON result
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = handleExec("stream-target", "echo streaming", 5, true)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("handleExec stream: %v", err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "chunk-data") {
		t.Errorf("output should contain stream chunk: %s", output)
	}
	if !strings.Contains(output, "complete") {
		t.Errorf("output should contain complete event: %s", output)
	}
}

func TestIntegrationClientHandleExecError(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	tmpDir, err := os.MkdirTemp("", "remotecmd-client-err-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("unknown-target", "tok")

	// Exec on target that isn't connected — handleExec returns nil because
	// the error result is printed to stdout as JSON, not returned as a Go error.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = handleExec("unknown-target", "echo hi", 5, false)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("handleExec should not return Go error for relay errors: %v", err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "not connected") {
		t.Errorf("output should contain 'not connected': %s", output)
	}
	if !strings.Contains(output, `"ok": false`) {
		t.Errorf("output should indicate failure: %s", output)
	}
}

func TestIntegrationClientHandleMultiExec(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	// Two daemons
	d1 := testDaemon(t, "http://127.0.0.1:"+itoa(port), "node-x", "tx")
	defer d1.Close()
	testDaemonResponder(t, d1, 0, "host-x")

	d2 := testDaemon(t, "http://127.0.0.1:"+itoa(port), "node-y", "ty")
	defer d2.Close()
	testDaemonResponder(t, d2, 0, "host-y")

	time.Sleep(50 * time.Millisecond)

	tmpDir, err := os.MkdirTemp("", "remotecmd-multi-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("node-x", "tx")
	addTarget("node-y", "ty")

	// Test table format
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = handleMultiExec([]string{"node-x", "node-y"}, "hostname", 5, "table")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("handleMultiExec: %v", err)
	}

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "host-x") {
		t.Errorf("should contain node-x output: %s", output)
	}
	if !strings.Contains(output, "host-y") {
		t.Errorf("should contain node-y output: %s", output)
	}
	if !strings.Contains(output, "OK") {
		t.Errorf("should show OK status: %s", output)
	}
}

func TestIntegrationClientHandleMultiExecJSON(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "json-node", "jtok")
	defer daemon.Close()
	testDaemonResponder(t, daemon, 0, "json-output")

	time.Sleep(50 * time.Millisecond)

	tmpDir, err := os.MkdirTemp("", "remotecmd-multi-json-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("json-node", "jtok")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = handleMultiExec([]string{"json-node"}, "hostname", 5, "json")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("handleMultiExec json: %v", err)
	}

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "multi_result") {
		t.Errorf("json output should contain multi_result: %s", output)
	}
	if !strings.Contains(output, "json-output") {
		t.Errorf("json output should contain daemon response: %s", output)
	}
}

func TestIntegrationClientHandleMultiExecPartialFailure(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	d1 := testDaemon(t, "http://127.0.0.1:"+itoa(port), "ok-node", "okt")
	defer d1.Close()
	testDaemonResponder(t, d1, 0, "ok-output")

	time.Sleep(50 * time.Millisecond)

	tmpDir, err := os.MkdirTemp("", "remotecmd-multi-partial-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("ok-node", "okt")
	addTarget("missing-node", "mtok")

	// Should return error because missing-node is not connected
	err = handleMultiExec([]string{"ok-node", "missing-node"}, "hostname", 5, "table")
	if err == nil {
		t.Error("expected error for partial failure")
	}
	if err != nil && !strings.Contains(err.Error(), "failed") {
		t.Errorf("expected 'targets failed' error, got: %v", err)
	}
}

func TestIntegrationHandleMultiExecTargetOrder(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "only-node", "otok")
	defer daemon.Close()
	testDaemonResponder(t, daemon, 0, "only-output")

	time.Sleep(50 * time.Millisecond)

	tmpDir, err := os.MkdirTemp("", "remotecmd-multi-order-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("only-node", "otok")

	// Single target via multi-exec should work
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = handleMultiExec([]string{"only-node"}, "hostname", 5, "table")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("handleMultiExec single: %v", err)
	}

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "only-output") {
		t.Errorf("should contain target output: %s", output)
	}
}

func TestIntegrationHandleMultiExecNoRelay(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-multi-norelay-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// No relay configured
	err = handleMultiExec([]string{"target"}, "cmd", 5, "json")
	if err == nil {
		t.Error("expected error when relay not configured")
	}
}

func TestIntegrationHandleExecNoRelay(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-exec-norelay-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	err = handleExec("target", "cmd", 5, false)
	if err == nil {
		t.Error("expected error when relay not configured")
	}
}

func TestIntegrationHandleExecUnknownTarget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-exec-unk-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:9999", "test-client")
	// No target added

	err = handleExec("nonexistent", "cmd", 5, false)
	if err == nil {
		t.Error("expected error for unknown target")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Errorf("expected 'unknown target' error, got: %v", err)
	}
}

func TestIntegrationHandleMultiExecUnknownTarget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-multi-unk-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:9999", "test-client")
	// No target added

	err = handleMultiExec([]string{"nonexistent"}, "cmd", 5, "json")
	if err == nil {
		t.Error("expected error for unknown target")
	}
}

func TestIntegrationFileTransferViaHandleFileTransfer(t *testing.T) {
	rs, port := startTestRelay(t)
	defer func() { _ = rs }()

	daemon := testDaemon(t, "http://127.0.0.1:"+itoa(port), "ft-target", "fttok")
	defer daemon.Close()

	go func() {
		for {
			var msg Message
			if err := daemon.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "file_transfer" {
				daemon.WriteJSON(&Message{Type: "file_transfer_result", ID: msg.ID, OK: boolPtr(true)})
			}
			if msg.Type == "command" {
				daemon.WriteJSON(&Message{Type: "result", ID: msg.ID, OK: boolPtr(true), Stdout: "ok", ExitCode: 0, DurationMs: 1})
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	tmpDir, err := os.MkdirTemp("", "remotecmd-ft-test-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	setRelay("http://127.0.0.1:"+itoa(port), "test-client")
	addTarget("ft-target", "fttok")

	// Create a source file
	srcFile := filepath.Join(tmpDir, "source.txt")
	os.WriteFile(srcFile, []byte("test-content"), 0644)

	// Call handleFileTransfer directly (prints success via handleCP)
	// handleFileTransfer returns nil on success, the success message is
	// printed by handleCP which calls handleFileTransfer
	err = handleFileTransfer("ft-target", srcFile, "/remote/dest.txt", false)

	if err != nil {
		t.Fatalf("handleFileTransfer: %v", err)
	}

	// handleFileTransfer returns nil on success — the success message
	// is printed by handleCP which wraps handleFileTransfer
	// Just verify no error
}
