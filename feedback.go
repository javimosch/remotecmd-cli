package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// handleFeedback implements `rcmd feedback <message> [--kind bug|idea|praise] [--context <c>]`.
//
// remotecmd is a peer tool with no central store, so this is the machin-feedback contract's
// "relay-only" adoption: best-effort POST to the central relay (machin-feedback) tagged app=rcmd.
// Never fails the caller; reports {ok,id,relayed}. Override the relay with REMOTECMD_FEEDBACK_RELAY
// (or "off" to disable).
func handleFeedback(args []string) {
	if len(args) < 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, `usage: rcmd feedback "<message>" [--kind bug|idea|praise] [--context "<what you were doing>"]`)
		osExit(ExitConfigError)
		return
	}
	msg := args[0]
	kind := "note"
	context := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--context":
			if i+1 < len(args) {
				context = args[i+1]
				i++
			}
		}
	}

	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	id := hex.EncodeToString(idb)

	reporter := os.Getenv("USER")
	if reporter == "" {
		reporter = "agent"
	}

	payload, _ := json.Marshal(map[string]string{
		"app":      "rcmd",
		"version":  Version,
		"kind":     kind,
		"message":  msg,
		"context":  context,
		"reporter": reporter,
		"id":       id,
	})

	relay := os.Getenv("REMOTECMD_FEEDBACK_RELAY")
	if relay == "" {
		relay = "https://feedback.intrane.fr"
	}

	relayed := false
	if relay != "off" {
		client := &http.Client{Timeout: 5 * time.Second}
		if resp, err := client.Post(relay+"/v1/feedback", "application/json", bytes.NewReader(payload)); err == nil {
			relayed = resp.StatusCode == 200
			_ = resp.Body.Close()
		}
	}

	out, _ := json.Marshal(map[string]any{"ok": true, "id": id, "relayed": relayed})
	fmt.Println(string(out))
}
