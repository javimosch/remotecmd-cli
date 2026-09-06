//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

var (
	relayPidFile  = os.TempDir() + string(os.PathSeparator) + "remotecmd-relay.pid"
	relayLogFile  = os.TempDir() + string(os.PathSeparator) + "remotecmd-relay.log"
	daemonPidFile = os.TempDir() + string(os.PathSeparator) + "remotecmd-daemon.pid"
	daemonLogFile = os.TempDir() + string(os.PathSeparator) + "remotecmd-daemon.log"
)

// DETACHED_PROCESS and CREATE_NEW_PROCESS_GROUP flags for Windows background processes.
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

func startBackground(pidFile, logFile string, args ...string) error {
	if running, _ := isRunning(pidFile); running {
		return fmt.Errorf("already running (PID file: %s)", pidFile)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer lf.Close()

	cmd := exec.Command(execPath, args...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	// Detach the process so it survives the parent session closing.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	pidData := []byte(strconv.Itoa(cmd.Process.Pid))
	if err := os.WriteFile(pidFile, pidData, 0644); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("write PID file: %w", err)
	}

	return nil
}

func stopBackground(pidFile string) error {
	running, pid := isRunning(pidFile)
	if !running {
		os.Remove(pidFile)
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidFile)
		return nil
	}

	proc.Kill()
	os.Remove(pidFile)
	return nil
}

func statusBackground(pidFile string) (bool, int) {
	running, pid := isRunning(pidFile)
	if running {
		fmt.Printf("PID: %d\n", pid)
	} else {
		fmt.Println("Not running")
	}
	return running, pid
}

func isRunning(pidFile string) (bool, int) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(pidFile)
		return false, 0
	}

	// On Windows, os.FindProcess always succeeds and Signal(0) is not supported.
	// Use OpenProcess with SYNCHRONIZE to check if the PID is actually alive.
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// Process doesn't exist or we don't have access — treat as not running.
		os.Remove(pidFile)
		return false, 0
	}
	syscall.CloseHandle(handle)

	return true, pid
}

func readPid(pidFile string) int {
	data, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}
