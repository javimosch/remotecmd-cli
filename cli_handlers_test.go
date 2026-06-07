package main

import (
	"os"
	"strings"
	"testing"
)

func TestHandleListTargetsWithConfig(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("server-a", "tok-a")
	addTarget("server-b", "tok-b")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleListTargets()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "server-a") {
		t.Errorf("should list server-a: %s", output)
	}
	if !strings.Contains(output, "server-b") {
		t.Errorf("should list server-b: %s", output)
	}
}

func TestHandleListTargetsWithGroups(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "tw1")
	groupCreate("web", []string{"web1"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleListTargets()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "Groups:") {
		t.Errorf("should show Groups section: %s", output)
	}
}

func TestHandleListTargetsEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleListTargets()

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "No targets") {
		t.Errorf("expected 'No targets': %s", output)
	}
}

func TestHandleGroupListWithEntries(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("n1", "t1")
	addTarget("n2", "t2")
	groupCreate("my-group", []string{"n1", "n2"})
	groupCreate("other-group", []string{"n1"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroupList()

	w.Close()
	os.Stdout = old

	var buf [512]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "my-group") {
		t.Errorf("should show my-group: %s", output)
	}
	if !strings.Contains(output, "other-group") {
		t.Errorf("should show other-group: %s", output)
	}
}

func TestHandleGroupListEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroupList()

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "No groups") {
		t.Errorf("expected 'No groups': %s", output)
	}
}

func TestHandleAddTargetValidation(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// This calls os.Exit, so we can't test the error path directly.
	// But the handler itself is a thin wrapper around addTarget.
	// addTarget is already tested in config_test.go.
}

func TestHandleSetRelayValidation(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Success path
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSetRelay([]string{"--url", "http://relay:3032", "--name", "node1"})

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "node1") {
		t.Errorf("expected success message: %s", output)
	}

	cfg, _ := loadConfig()
	if cfg.Relay.URL != "http://relay:3032" {
		t.Errorf("relay URL = %q", cfg.Relay.URL)
	}
}

func TestHandleRemoveTargetSuccess(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("remove-me", "rtok")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleRemoveTarget([]string{"--name", "remove-me"})

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "removed") {
		t.Errorf("expected removal message: %s", output)
	}

	cfg, _ := loadConfig()
	if _, ok := cfg.Targets["remove-me"]; ok {
		t.Error("target should be removed")
	}
}
