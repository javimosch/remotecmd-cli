package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleAddTarget(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleAddTarget([]string{"--name", "testbox", "--token", "abc123"})

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "testbox") {
		t.Errorf("expected target name in output: %s", output)
	}

	// Verify config
	cfg, _ := loadConfig()
	if _, ok := cfg.Targets["testbox"]; !ok {
		t.Error("target should be in config")
	}
}

func TestHandleRemoveTarget(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("testbox", "abc123")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleRemoveTarget([]string{"--name", "testbox"})

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "removed") {
		t.Errorf("expected removal message: %s", output)
	}

	cfg, _ := loadConfig()
	if _, ok := cfg.Targets["testbox"]; ok {
		t.Error("target should be removed from config")
	}
}

func TestHandleSetRelay(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSetRelay([]string{"--url", "http://relay:3032", "--name", "mynode"})

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "mynode") {
		t.Errorf("expected node name in output: %s", output)
	}

	cfg, _ := loadConfig()
	if cfg.Relay.URL != "http://relay:3032" || cfg.Relay.Name != "mynode" {
		t.Errorf("relay config not set: %+v", cfg.Relay)
	}
}

func TestHandleListTargets(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("testbox", "abc123")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleListTargets()

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "testbox") {
		t.Errorf("expected target in listing: %s", output)
	}
}

func TestHandleGroupCreate(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	_ = os.Stdout // discard

	handleGroupCreate([]string{"--name", "web", "--targets", "web1,web2"})

	cfg, _ := loadConfig()
	group, ok := cfg.Groups["web"]
	if !ok {
		t.Fatal("group should exist")
	}
	if len(group) != 2 {
		t.Errorf("expected 2 members, got %v", group)
	}
}

func TestHandleGroupDelete(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	groupCreate("web", []string{"web1"})

	handleGroupDelete([]string{"--name", "web"})

	cfg, _ := loadConfig()
	if _, ok := cfg.Groups["web"]; ok {
		t.Error("group should be deleted")
	}
}

func TestHandleGroupAdd(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	groupCreate("web", []string{"web1"})

	handleGroupAdd([]string{"--name", "web", "--targets", "web2"})

	cfg, _ := loadConfig()
	group := cfg.Groups["web"]
	if len(group) != 2 {
		t.Errorf("expected 2 members, got %v", group)
	}
}

func TestHandleGroupRemove(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	addTarget("web2", "t2")
	groupCreate("web", []string{"web1", "web2"})

	handleGroupRemove([]string{"--name", "web", "--targets", "web1"})

	cfg, _ := loadConfig()
	group := cfg.Groups["web"]
	if len(group) != 1 || group[0] != "web2" {
		t.Errorf("expected [web2], got %v", group)
	}
}

func TestHandleGroupList(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")
	groupCreate("web", []string{"web1"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroupList()

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "web:") {
		t.Errorf("expected group listing: %s", output)
	}
}

func TestHandleGroupSubcommand(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")

	// Test list with no groups
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroupSubcommand([]string{"list"})

	w.Close()
	os.Stdout = old
	r.Close() // discard
}

func TestConfigDirPath(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// ensureConfigDir creates the dir
	err := ensureConfigDir()
	if err != nil {
		t.Fatalf("ensureConfigDir: %v", err)
	}

	// Verify paths
	if _, err := os.Stat(configDir()); os.IsNotExist(err) {
		t.Error("config dir should exist")
	}
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Log("config path exists (may or may not be created)")
	}

	_ = filepath.Join // ensure imported
}
