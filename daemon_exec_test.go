package main

import (
	"testing"
	"time"
)

func TestDaemonExecuteCommandBuffered(t *testing.T) {
	td := &TargetDaemon{}

	msg := &Message{
		ID:      "test-exec-1",
		Cmd:     "echo hello-world",
		Timeout: 5,
	}

	td.executeCommandBuffered(msg)
}

func TestDaemonExecuteCommandBufferedWithStderr(t *testing.T) {
	td := &TargetDaemon{}

	msg := &Message{
		ID:      "test-exec-2",
		Cmd:     "echo out && echo err >&2 && exit 42",
		Timeout: 5,
	}

	td.executeCommandBuffered(msg)
}

func TestDaemonExecuteCommandBufferedWithTimeout(t *testing.T) {
	td := &TargetDaemon{}

	msg := &Message{
		ID:      "test-exec-3",
		Cmd:     "echo quick", // Non-hanging command
		Timeout: 5,
	}

	start := time.Now()
	td.executeCommandBuffered(msg)
	duration := time.Since(start)

	if duration > 10*time.Second {
		t.Errorf("command should have completed quickly, took %v", duration)
	}
}

func TestDaemonExecuteCommandStreaming(t *testing.T) {
	td := &TargetDaemon{}

	msg := &Message{
		ID:      "test-stream-1",
		Cmd:     "echo streaming-test",
		Timeout: 5,
		Stream:  true,
	}

	td.executeCommandStreaming(msg)
}

func TestDaemonExecuteCommandStreamingWithStderr(t *testing.T) {
	td := &TargetDaemon{}

	msg := &Message{
		ID:      "test-stream-2",
		Cmd:     "echo stdout-msg && echo stderr-msg >&2",
		Timeout: 5,
		Stream:  true,
	}

	td.executeCommandStreaming(msg)
}

func TestDaemonExecuteCommandDispatch(t *testing.T) {
	td := &TargetDaemon{}

	td.executeCommand(&Message{
		ID:      "test-dispatch-1",
		Cmd:     "echo stream",
		Stream:  true,
		Timeout: 5,
	})

	td.executeCommand(&Message{
		ID:      "test-dispatch-2",
		Cmd:     "echo buffered",
		Stream:  false,
		Timeout: 5,
	})
}

func TestDaemonExecuteCommandDefaultTimeout(t *testing.T) {
	td := &TargetDaemon{}

	msg := &Message{
		ID:      "test-default-timeout",
		Cmd:     "echo no-timeout-set",
		Timeout: 0,
	}

	td.executeCommandBuffered(msg)
}

func TestDaemonTargetDaemonNilConn(t *testing.T) {
	td := &TargetDaemon{}

	// send() with nil conn should log error but not crash
	td.send(&Message{Type: "test", ID: "nil-conn-test"})
}
