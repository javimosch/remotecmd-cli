package main

import (
	"os"
	"strings"
	"testing"
)

func TestPrintHelp(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printHelp()

	w.Close()
	os.Stdout = old

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	sections := []string{
		"EXECUTE", "FILE TRANSFER", "TARGET CONFIGURATION",
		"GROUP MANAGEMENT", "ALIAS", "RELAY", "DAEMON",
		"PAIRING", "OTHER",
	}
	for _, s := range sections {
		if !strings.Contains(output, s) {
			t.Errorf("help should contain section %q", s)
		}
	}

	if !strings.Contains(output, "exec --targets") {
		t.Errorf("help should mention exec --targets")
	}
	if !strings.Contains(output, "exec --group") {
		t.Errorf("help should mention exec --group")
	}
	if !strings.Contains(output, "group create") {
		t.Errorf("help should mention group create")
	}
}
