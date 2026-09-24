package main

import (
	"testing"
	"time"
)

// waitFor polls cond every 100ms for up to 10s. Shared by the Unix-only
// restart tests and the cross-platform tunnel tests, so it lives in an
// untagged file.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
