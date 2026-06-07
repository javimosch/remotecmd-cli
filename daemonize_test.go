package main

import (
	"os"
	"strconv"
	"testing"
)

func TestIsRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-pid-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// No PID file
	running, pid := isRunning(pidFile)
	if running {
		t.Error("expected not running with no PID file")
	}
	if pid != 0 {
		t.Errorf("expected pid 0, got %d", pid)
	}

	// Valid PID file (current process)
	currentPID := os.Getpid()
	os.WriteFile(pidFile, []byte(strconv.Itoa(currentPID)), 0644)

	running, pid = isRunning(pidFile)
	if !running {
		t.Error("current process should be running")
	}
	if pid != currentPID {
		t.Errorf("expected pid %d, got %d", currentPID, pid)
	}

	// Invalid PID (non-existent process)
	os.WriteFile(pidFile, []byte("999999999"), 0644)
	running, _ = isRunning(pidFile)
	if running {
		t.Error("non-existent process should not be running")
	}

	// Invalid PID file content
	os.WriteFile(pidFile, []byte("not-a-number"), 0644)
	running, _ = isRunning(pidFile)
	if running {
		t.Error("should not be running with invalid PID file content")
	}

	// Empty PID file
	os.WriteFile(pidFile, []byte(""), 0644)
	running, _ = isRunning(pidFile)
	if running {
		t.Error("should not be running with empty PID file")
	}
}

func TestReadPid(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-readpid-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Non-existent file
	pid := readPid(pidFile)
	if pid != 0 {
		t.Errorf("expected 0 for non-existent file, got %d", pid)
	}

	// Valid PID
	os.WriteFile(pidFile, []byte("12345\n"), 0644)
	pid = readPid(pidFile)
	if pid != 12345 {
		t.Errorf("expected 12345, got %d", pid)
	}

	// Invalid content
	os.WriteFile(pidFile, []byte("abc"), 0644)
	pid = readPid(pidFile)
	if pid != 0 {
		t.Errorf("expected 0 for invalid content, got %d", pid)
	}
}

func TestStatusBackground(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-status-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Not running
	running, pid := statusBackground(pidFile)

	if running {
		t.Error("expected not running")
	}
	if pid != 0 {
		t.Errorf("expected pid 0, got %d", pid)
	}
}
