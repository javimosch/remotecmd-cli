package main

import (
	"os"
	"strings"
	"testing"
)

func TestExecStreamingWithExit(t *testing.T) {
	td := &TargetDaemon{}
	td.executeCommandStreaming(&Message{
		ID:      "stream-exit",
		Cmd:     "echo ok && exit 5",
		Timeout: 5,
		Stream:  true,
	})
}

func TestExecStreamingWithNoOutput(t *testing.T) {
	td := &TargetDaemon{}
	td.executeCommandStreaming(&Message{
		ID:      "stream-noout",
		Cmd:     "exit 0",
		Timeout: 5,
		Stream:  true,
	})
}

func TestExecBufferedWithNoOutput(t *testing.T) {
	td := &TargetDaemon{}
	td.executeCommandBuffered(&Message{
		ID:      "buffered-noout",
		Cmd:     "exit 0",
		Timeout: 5,
	})
}

func TestExecBufferedWithLongOutput(t *testing.T) {
	td := &TargetDaemon{}
	td.executeCommandBuffered(&Message{
		ID:      "buffered-long",
		Cmd:     "for i in $(seq 1 100); do echo line-$i; done",
		Timeout: 5,
	})
}

func TestStreamingWithLongOutput(t *testing.T) {
	td := &TargetDaemon{}
	td.executeCommandStreaming(&Message{
		ID:      "stream-long",
		Cmd:     "for i in $(seq 1 10); do echo line-$i; done",
		Timeout: 5,
		Stream:  true,
	})
}

func TestMultipleConfigTargetsList(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("a1", "tok1")
	addTarget("a2", "tok2")
	addTarget("a3", "tok3")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	listTargets()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "a1") || !strings.Contains(output, "a3") {
		t.Errorf("should list all targets: %s", output)
	}
}

func TestHandleSetRelayOverwrites(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	handleSetRelay([]string{"--url", "http://r1:3032", "--name", "n1"})
	handleSetRelay([]string{"--url", "http://r2:3032", "--name", "n2"})

	cfg, _ := loadConfig()
	if cfg.Relay.URL != "http://r2:3032" {
		t.Errorf("URL = %q, want http://r2:3032", cfg.Relay.URL)
	}
}
