//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// spawnDetached starts bin in its own session with output to logPath, and
// does not wait for it: it must outlive the process that launched it.
func spawnDetached(bin string, args []string, logPath string) error {
	_, err := startDetached(append([]string{bin}, args...), os.Environ(), "", logPath)
	return err
}

// startDetached starts argv in a new session and returns its PID.
func startDetached(argv, env []string, dir, logPath string) (int, error) {
	out, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, dir
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go cmd.Wait() // reap it if we are still around when it exits
	return pid, nil
}

func terminateProcess(pid int) { syscall.Kill(pid, syscall.SIGTERM) }

func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// A zombie still answers kill(0); treat it as gone.
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err != nil || !zombie(string(b))
}
