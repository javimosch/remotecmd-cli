//go:build windows

package main

import (
	"fmt"
)

// listenForPairSignal is a no-op on Windows (no SIGUSR1).
// The daemon re-checks pair codes automatically every 15 seconds.
func listenForPairSignal(td *TargetDaemon) {
	// No-op — pair codes are picked up automatically within 15 seconds.
}

// sendPairSignal is a no-op on Windows (no SIGUSR1).
// The pair code will be picked up automatically within 15 seconds.
func sendPairSignal(pid int) error {
	return fmt.Errorf("signal not supported on Windows (pair code picked up within 15s)")
}
