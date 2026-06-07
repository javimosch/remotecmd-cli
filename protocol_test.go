package main

import (
	"testing"
)

func TestNewID(t *testing.T) {
	id1 := newID()
	id2 := newID()

	if len(id1) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(id1), id1)
	}
	if len(id2) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(id2), id2)
	}
	if id1 == id2 {
		t.Error("two sequential IDs should be different")
	}
}

func TestWsURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://example.com:3032", "ws://example.com:3032"},
		{"https://example.com:3032", "wss://example.com:3032"},
		{"ws://example.com:3032", "ws://example.com:3032"},
		{"wss://example.com:3032", "wss://example.com:3032"},
		{"example.com:3032", "ws://example.com:3032"},
		{"", "ws://"},
		{"http://192.168.1.1:8080", "ws://192.168.1.1:8080"},
	}

	for _, tt := range tests {
		result := wsURL(tt.input)
		if result != tt.expected {
			t.Errorf("wsURL(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestOkResult(t *testing.T) {
	msg := okResult("test-id", "stdout-data", "stderr-data", 0, 1234)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Type != "result" {
		t.Errorf("Type = %q, want %q", msg.Type, "result")
	}
	if msg.ID != "test-id" {
		t.Errorf("ID = %q, want %q", msg.ID, "test-id")
	}
	if msg.OK == nil || !*msg.OK {
		t.Error("OK should be true")
	}
	if msg.Stdout != "stdout-data" {
		t.Errorf("Stdout = %q, want %q", msg.Stdout, "stdout-data")
	}
	if msg.Stderr != "stderr-data" {
		t.Errorf("Stderr = %q, want %q", msg.Stderr, "stderr-data")
	}
	if msg.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", msg.ExitCode)
	}
	if msg.DurationMs != 1234 {
		t.Errorf("DurationMs = %d, want 1234", msg.DurationMs)
	}

	// okResult always indicates successful delivery, separate from exit code
	msg2 := okResult("id2", "out", "err", 1, 567)
	if msg2.OK == nil || !*msg2.OK {
		t.Error("okResult should always set OK=true (delivery success, not exit code)")
	}
	if msg2.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", msg2.ExitCode)
	}
}

func TestErrResult(t *testing.T) {
	msg := errResult("err-id", "something went wrong")

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Type != "result" {
		t.Errorf("Type = %q, want %q", msg.Type, "result")
	}
	if msg.ID != "err-id" {
		t.Errorf("ID = %q, want %q", msg.ID, "err-id")
	}
	if msg.OK == nil || *msg.OK {
		t.Error("OK should be false")
	}
	if msg.Error != "something went wrong" {
		t.Errorf("Error = %q, want %q", msg.Error, "something went wrong")
	}
}

func TestStreamEndOK(t *testing.T) {
	msg := streamEndOK("stream-id", 0, 500)

	if msg.Type != "stream_end" {
		t.Errorf("Type = %q, want %q", msg.Type, "stream_end")
	}
	if msg.ID != "stream-id" {
		t.Errorf("ID = %q, want %q", msg.ID, "stream-id")
	}
	if msg.OK == nil || !*msg.OK {
		t.Error("OK should be true for exit 0")
	}
	if msg.DurationMs != 500 {
		t.Errorf("DurationMs = %d, want 500", msg.DurationMs)
	}

	// Non-zero exit code
	msg2 := streamEndOK("s2", 1, 600)
	if msg2.OK == nil || *msg2.OK {
		t.Error("OK should be false for exit 1")
	}
}

func TestStreamEndErr(t *testing.T) {
	msg := streamEndErr("err-stream", "command failed")

	if msg.Type != "stream_end" {
		t.Errorf("Type = %q, want %q", msg.Type, "stream_end")
	}
	if msg.ID != "err-stream" {
		t.Errorf("ID = %q, want %q", msg.ID, "err-stream")
	}
	if msg.OK == nil || *msg.OK {
		t.Error("OK should be false")
	}
	if msg.Error != "command failed" {
		t.Errorf("Error = %q, want %q", msg.Error, "command failed")
	}
}
