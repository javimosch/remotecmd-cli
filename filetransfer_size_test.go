package main

import (
	"strings"
	"testing"
)

// TestMaxTransferBytesDefault verifies the default per-transfer limit when no
// override is set.
func TestMaxTransferBytesDefault(t *testing.T) {
	t.Setenv("REMOTECMD_MAX_TRANSFER_MB", "")
	if got := maxTransferBytes(); got != defaultMaxTransferBytes {
		t.Errorf("maxTransferBytes() = %d, want default %d", got, defaultMaxTransferBytes)
	}
}

// TestMaxTransferBytesOverride verifies a valid override is honored and that a
// zero disables the limit.
func TestMaxTransferBytesOverride(t *testing.T) {
	t.Setenv("REMOTECMD_MAX_TRANSFER_MB", "10")
	if got, want := maxTransferBytes(), int64(10<<20); got != want {
		t.Errorf("override 10 => %d, want %d", got, want)
	}

	t.Setenv("REMOTECMD_MAX_TRANSFER_MB", "0")
	if got := maxTransferBytes(); got != 0 {
		t.Errorf("override 0 (disabled) => %d, want 0", got)
	}
}

// TestMaxTransferBytesInvalidOverrideFallsBack verifies that a malformed or
// negative override is ignored in favor of the default rather than producing a
// nonsensical limit.
func TestMaxTransferBytesInvalidOverrideFallsBack(t *testing.T) {
	for _, bad := range []string{"abc", "-5", "12.5"} {
		t.Setenv("REMOTECMD_MAX_TRANSFER_MB", bad)
		if got := maxTransferBytes(); got != defaultMaxTransferBytes {
			t.Errorf("override %q => %d, want default %d", bad, got, defaultMaxTransferBytes)
		}
	}
}

// TestCheckTransferSizeUnderLimit verifies payloads at or below the limit pass.
func TestCheckTransferSizeUnderLimit(t *testing.T) {
	t.Setenv("REMOTECMD_MAX_TRANSFER_MB", "100")
	if err := checkTransferSize(50<<20, "file"); err != nil {
		t.Errorf("50MB under 100MB limit should pass, got %v", err)
	}
	if err := checkTransferSize(100<<20, "file"); err != nil {
		t.Errorf("exactly 100MB should pass, got %v", err)
	}
}

// TestCheckTransferSizeOverLimit verifies oversized payloads return a clear,
// actionable error naming the size, the limit, and a workaround — instead of the
// silent relay stall in issue #1.
func TestCheckTransferSizeOverLimit(t *testing.T) {
	t.Setenv("REMOTECMD_MAX_TRANSFER_MB", "100")
	err := checkTransferSize(250<<20, "file")
	if err == nil {
		t.Fatal("250MB over 100MB limit should error")
	}
	msg := err.Error()
	for _, want := range []string{"250 MB", "100 MB", "split", "REMOTECMD_MAX_TRANSFER_MB"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// TestCheckTransferSizeDisabled verifies that a zero limit disables the guard so
// self-hosted relays with larger frame limits are not blocked.
func TestCheckTransferSizeDisabled(t *testing.T) {
	t.Setenv("REMOTECMD_MAX_TRANSFER_MB", "0")
	if err := checkTransferSize(5<<30, "file"); err != nil {
		t.Errorf("limit disabled (0) should allow any size, got %v", err)
	}
}
