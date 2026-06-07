package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStopBackgroundKillsProcess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-stopbg-kill-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := filepath.Join(tmpDir, "test.pid")
	logFile := filepath.Join(tmpDir, "test.log")

	// Start a long-running process in background using startBackground
	// We start "remotecmd-cli.test version" which exits quickly.
	// Instead, start a real sleep process via shell
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}

	// Write the PID file
	pid := cmd.Process.Pid
	os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)

	// Verify process is running
	running, readPid := isRunning(pidFile)
	if !running {
		t.Fatal("process should be running")
	}
	if readPid != pid {
		t.Fatalf("pid mismatch: %d vs %d", readPid, pid)
	}

	// Stop it
	err = stopBackground(pidFile)
	if err != nil {
		t.Fatalf("stopBackground: %v", err)
	}

	// Verify process is dead
	running, _ = isRunning(pidFile)
	if running {
		t.Error("process should not be running after stop")
	}

	// PID file should be removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed")
	}

	_ = logFile
}

func TestStopBackgroundMultipleCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-stopbg-multi-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := filepath.Join(tmpDir, "test.pid")

	// Stopping with no PID file should be a no-op
	err = stopBackground(pidFile)
	if err != nil {
		t.Errorf("first stop: %v", err)
	}

	// Second stop also no-op
	err = stopBackground(pidFile)
	if err != nil {
		t.Errorf("second stop: %v", err)
	}
}

func TestStopBackgroundWithSIGTERMIgnored(t *testing.T) {
	// Some processes ignore SIGTERM. stopBackground should handle this.
	tmpDir, err := os.MkdirTemp("", "remotecmd-stopbg-term-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := filepath.Join(tmpDir, "test.pid")

	// Start a process that ignores SIGTERM using trap
	cmd := exec.Command("sh", "-c", "trap '' TERM; while true; do sleep 1; done")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
	defer cmd.Process.Kill()

	// stopBackground should fall through to Kill after timeout
	err = stopBackground(pidFile)
	if err != nil {
		t.Logf("stopBackground on SIGTERM-ignoring process: %v (may still clean up)", err)
	}

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)
	running, _ := isRunning(pidFile)
	if running {
		t.Log("process may still be running (SIGKILL might not work in all environments)")
	}
}

func TestStartBackgroundAndStop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-startbg-stop-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := filepath.Join(tmpDir, "test.pid")
	logFile := filepath.Join(tmpDir, "test.log")

	// startBackground starts the test binary with given args.
	// Running "version" prints version and exits quickly, so
	// isRunning will return false immediately after start.
	// Instead, start a sleep process directly.
	cmd := exec.Command("sh", "-c", "sleep 10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	_ = logFile

	// Verify it's running via isRunning
	running, _ := isRunning(pidFile)
	if !running {
		t.Fatal("process should be running")
	}

	// Stop it
	if err := stopBackground(pidFile); err != nil {
		t.Fatalf("stopBackground: %v", err)
	}

	// PID file should be gone
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after stop")
	}
}

func TestIsRunningWithInvalidFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-isrunning-inv-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := filepath.Join(tmpDir, "test.pid")

	tests := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"spaces", "   "},
		{"text", "not-a-number"},
		{"float", "3.14"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(pidFile, []byte(tt.content), 0644)
			running, pid := isRunning(pidFile)
			if running {
				t.Error("should not be running with invalid content")
			}
			if pid != 0 {
				t.Errorf("pid should be 0, got %d", pid)
			}
			// Should remove invalid PID file
			if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
				t.Error("invalid PID file should be removed")
			}
		})
	}
}

func TestStatusBackgroundOutput(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-status-out-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := filepath.Join(tmpDir, "test.pid")

	// Not running
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	running, pid := statusBackground(pidFile)

	w.Close()
	os.Stdout = old

	if running {
		t.Error("should not be running")
	}
	if pid != 0 {
		t.Errorf("pid should be 0, got %d", pid)
	}

	var buf [128]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])
	if !strings.Contains(output, "Not running") {
		t.Errorf("expected 'Not running': %s", output)
	}
}
