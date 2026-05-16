package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	relayPidFile = "/tmp/remotecmd-relay.pid"
	relayLogFile = "/tmp/remotecmd-relay.log"
	daemonPidFile = "/tmp/remotecmd-daemon.pid"
	daemonLogFile = "/tmp/remotecmd-daemon.log"
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		os.Remove(pidFile)
		return nil
	}

	for i := 0; i < 10; i++ {
		running, _ = isRunning(pidFile)
		if !running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
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

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidFile)
		return false, 0
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidFile)
		return false, 0
	}

	return true, pid
}

func readPid(pidFile string) int {
	data, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}
