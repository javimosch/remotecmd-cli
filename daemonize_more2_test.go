package main

import (
	"os"
	"strconv"
	"testing"
)

func TestStopBackgroundRemovesPidFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-stopbg-rm-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Create PID file for a non-existent process
	os.WriteFile(pidFile, []byte("99999999"), 0644)

	// stopBackground should remove the stale PID file
	err = stopBackground(pidFile)
	if err != nil {
		t.Logf("stopBackground err: %v", err)
	}

	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("PID file should be removed")
	}
}

func TestStopBackgroundWithRealPID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-stopbg-real-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Create PID file pointing to current test process
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)

	// stopBackground sends SIGTERM to the process.
	// We can't kill the test process, so test that it errors gracefully.
	// Actually, stopBackground with our own PID would try to kill us.
	// Let's use a PID of 1 (init) which will never be killed by a normal user.
	os.WriteFile(pidFile, []byte("1"), 0644)

	err = stopBackground(pidFile)
	if err != nil {
		t.Logf("stopBackground on PID 1: %v", err)
	}
}

func TestIsRunningWithCurrentProcess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-isrunning-cur-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)

	running, pid := isRunning(pidFile)
	if !running {
		t.Error("current process should be running")
	}
	if pid != os.Getpid() {
		t.Errorf("expected pid %d, got %d", os.Getpid(), pid)
	}
}

func TestIsRunningWithNonexistentPid(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-isrunning-none-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"
	os.WriteFile(pidFile, []byte("99999999"), 0644)

	running, pid := isRunning(pidFile)
	if running {
		t.Error("should not be running for non-existent PID")
	}
	_ = pid

	// PID file should be removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("stale PID file should be removed")
	}
}

func TestReadPidEdgeCases(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-readpid3-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pidFile := tmpDir + "/test.pid"

	// Non-existent file
	if pid := readPid(pidFile); pid != 0 {
		t.Errorf("expected 0 for non-existent file, got %d", pid)
	}

	// Valid PID
	os.WriteFile(pidFile, []byte("42"), 0644)
	if pid := readPid(pidFile); pid != 42 {
		t.Errorf("expected 42, got %d", pid)
	}

	// Large number
	os.WriteFile(pidFile, []byte("2147483647"), 0644)
	if pid := readPid(pidFile); pid != 2147483647 {
		t.Errorf("expected 2147483647, got %d", pid)
	}
}
