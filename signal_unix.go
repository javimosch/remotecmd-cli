//go:build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// listenForPairSignal listens for SIGUSR1 and triggers a pair code re-check.
func listenForPairSignal(td *TargetDaemon) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	for range sigCh {
		log.Printf("Received SIGUSR1 — re-checking pair code")
		td.sendPairIfNeeded()
	}
}

// sendPairSignal sends SIGUSR1 to the daemon process to trigger immediate pair re-check.
func sendPairSignal(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGUSR1)
}

// listenForRestartSignal re-executes this process's binary in place on
// SIGUSR2: same PID, same arguments and environment, so systemd units,
// systemd user units and bare nohup processes all keep supervising it.
// Sent by `daemon update` / `relay daemon update` after swapping the binary.
// waitIdle, if set, runs first so in-flight work can finish.
func listenForRestartSignal(waitIdle func()) {
	// Resolve the path now, while /proc/self/exe still names the file we
	// were started from; an update replaces that path's contents, and we
	// re-exec whatever it holds then.
	exe, exeErr := os.Executable()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR2)
	for range sigCh {
		if exeErr != nil {
			log.Printf("Restart requested but executable path unknown: %v", exeErr)
			continue
		}
		log.Printf("Received SIGUSR2 — restarting in place from %s", exe)
		if waitIdle != nil {
			waitIdle()
		}
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			log.Printf("Restart failed, continuing on the current binary: %v", err)
		}
	}
}

// sendRestartSignal asks a running daemon or relay to re-exec itself.
func sendRestartSignal(pid int) error {
	return syscall.Kill(pid, syscall.SIGUSR2)
}
