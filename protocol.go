package main

import (
	"crypto/rand"
	"fmt"
)

type Message struct {
	Type       string `json:"type"`
	ID         string `json:"id,omitempty"`
	Target     string `json:"target,omitempty"`
	Token      string `json:"token,omitempty"`
	Name       string `json:"name,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	OK         *bool  `json:"ok,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

func okResult(id, stdout, stderr string, exitCode int, durationMs int64) *Message {
	b := true
	return &Message{
		Type:       "result",
		ID:         id,
		OK:         &b,
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	}
}

func errResult(id, msg string) *Message {
	b := false
	return &Message{
		Type:  "result",
		ID:    id,
		OK:    &b,
		Error: msg,
	}
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func wsURL(raw string) string {
	if len(raw) >= 7 && raw[:7] == "http://" {
		return "ws://" + raw[7:]
	}
	if len(raw) >= 8 && raw[:8] == "https://" {
		return "wss://" + raw[8:]
	}
	if len(raw) >= 5 && raw[:5] == "ws://" {
		return raw
	}
	if len(raw) >= 6 && raw[:6] == "wss://" {
		return raw
	}
	return "ws://" + raw
}
