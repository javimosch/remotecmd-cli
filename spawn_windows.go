//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// detachedProcess and createNewProcessGroup are defined in daemonize_windows.go.
const createBreakawayFromJob = 0x01000000

// spawnDetached starts bin detached, with output to logPath.
func spawnDetached(bin string, args []string, logPath string) error {
	_, err := startDetached(append([]string{bin}, args...), os.Environ(), "", logPath)
	return err
}

// startDetached starts argv outside our console and, when allowed, outside
// our job object: the updater usually runs inside a daemon started by a
// scheduled task, and a job ending must not take the new daemon with it.
func startDetached(argv, env []string, dir, logPath string) (int, error) {
	out, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env, cmd.Dir = env, dir
		cmd.Stdout, cmd.Stderr = out, out
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		return cmd, cmd.Start()
	}
	cmd, err := start(detachedProcess | createNewProcessGroup | createBreakawayFromJob)
	if err != nil { // the job may forbid breakaway
		cmd, err = start(detachedProcess | createNewProcessGroup)
	}
	if err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go cmd.Wait()
	return pid, nil
}

func terminateProcess(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid) // opens a handle; fails once it is gone
	if err != nil {
		return false
	}
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
		return false
	case <-time.After(50 * time.Millisecond):
		return true
	}
}
