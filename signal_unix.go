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
