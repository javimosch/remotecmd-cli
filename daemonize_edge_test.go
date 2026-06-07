package main

import (
	"os"
	"strconv"
	"testing"
)

func TestStartBackgroundAlreadyRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-bg-running-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"
	logFile := tmpDir + "/test.log"

	// Create a PID file pointing to current process
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)

	// Should fail because PID file exists and process is running
	err = startBackground(pidFile, logFile, "version")
	if err == nil {
		t.Error("expected error when process already running")
	}
	// Cleanup
	os.Remove(pidFile)
	os.Remove(logFile)
}

func TestStopBackgroundNoFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-bg-stop-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Stopping with no PID file should be a no-op
	err = stopBackground(pidFile)
	if err != nil {
		t.Errorf("stopBackground with no file should not error: %v", err)
	}
}

func TestStopBackgroundWithStalePID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-bg-stale-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Create a PID file pointing to a non-existent process
	os.WriteFile(pidFile, []byte("99999999"), 0644)

	err = stopBackground(pidFile)
	if err != nil {
		t.Errorf("stopBackground with stale PID should not error: %v", err)
	}

	// PID file should be removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after stop")
	}
}

func TestIsRunningNonExistentFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-isrunning-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	running, pid := isRunning(tmpDir + "/nonexistent.pid")
	if running {
		t.Error("should not be running")
	}
	if pid != 0 {
		t.Errorf("pid should be 0, got %d", pid)
	}
}

func TestIsRunningInvalidPIDContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-invalidpid-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	tests := []struct {
		name    string
		content string
	}{
		{"not-a-number", "not-a-number"},
		{"empty", ""},
		{"negative", "-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(pidFile, []byte(tt.content), 0644)
			running, _ := isRunning(pidFile)
			if running {
				t.Error("should not be running with invalid content")
			}
		})
	}
}
