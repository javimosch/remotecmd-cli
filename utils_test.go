package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEmitProgress(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	emitProgress("test_event", map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	})

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}

	if parsed["event"] != "test_event" {
		t.Errorf("event = %v, want %q", parsed["event"], "test_event")
	}

	data, ok := parsed["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data field to be an object")
	}
	if data["key1"] != "value1" {
		t.Errorf("data.key1 = %v, want %q", data["key1"], "value1")
	}
	if data["key2"] != float64(42) {
		t.Errorf("data.key2 = %v, want 42", data["key2"])
	}
}
