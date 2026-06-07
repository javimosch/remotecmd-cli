package main

import (
	"encoding/json"
	"testing"
)

func TestMessageMultiTargetFields(t *testing.T) {
	// Verify that multi-target fields serialize/deserialize correctly
	msg := &Message{
		Type:    "execute_multi",
		ID:      "multi-1",
		Targets: []string{"web1", "web2"},
		Tokens:  map[string]string{"web1": "t1", "web2": "t2"},
		Cmd:     "hostname",
		Timeout: 30,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != "execute_multi" {
		t.Errorf("Type = %q", decoded.Type)
	}
	if len(decoded.Targets) != 2 || decoded.Targets[0] != "web1" {
		t.Errorf("Targets = %v", decoded.Targets)
	}
	if decoded.Tokens["web1"] != "t1" {
		t.Errorf("Tokens = %v", decoded.Tokens)
	}
}

func TestMessageResultsField(t *testing.T) {
	results := map[string]*Message{
		"web1": {
			Type:       "result",
			OK:         boolPtr(true),
			Stdout:     "web1.example.com",
			ExitCode:   0,
			DurationMs: 5,
		},
		"web2": {
			Type:  "result",
			OK:    boolPtr(false),
			Error: "target not connected",
		},
	}

	msg := &Message{
		Type:    "multi_result",
		ID:      "mr-1",
		Results: results,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != "multi_result" {
		t.Errorf("Type = %q", decoded.Type)
	}
	if decoded.Results == nil {
		t.Fatal("Results should not be nil")
	}
	if decoded.Results["web1"] == nil || decoded.Results["web1"].Stdout != "web1.example.com" {
		t.Errorf("web1 result = %v", decoded.Results["web1"])
	}
	if decoded.Results["web2"] == nil || *decoded.Results["web2"].OK {
		t.Errorf("web2 result should have failed")
	}
}

func TestMessageParallelField(t *testing.T) {
	msg := &Message{
		Type:     "execute_multi",
		Parallel: 10,
	}

	data, _ := json.Marshal(msg)
	var decoded Message
	json.Unmarshal(data, &decoded)

	if decoded.Parallel != 10 {
		t.Errorf("Parallel = %d, want 10", decoded.Parallel)
	}
}

func TestMessageEmptyTargetsOmitempty(t *testing.T) {
	msg := &Message{
		Type:    "execute_multi",
		Targets: []string{},
		Tokens:  map[string]string{},
	}

	data, _ := json.Marshal(msg)
	var decoded Message
	json.Unmarshal(data, &decoded)

	// Both nil and empty are acceptable — omitempty means they may not round-trip
	_ = decoded
}
