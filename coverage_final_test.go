package main

import (
	"os"
	"strings"
	"testing"
)

func TestGroupSubcommandDispatch(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("test-a", "ta")
	addTarget("test-b", "tb")

	// Test through the dispatch function (cover dispatcher cases)
	handleGroupSubcommand([]string{"create", "--name", "g1", "--targets", "test-a,test-b"})
	cfg, _ := loadConfig()
	if len(cfg.Groups["g1"]) != 2 {
		t.Error("group should have 2 members")
	}

	// Add via dispatch
	addTarget("test-c", "tc")
	handleGroupSubcommand([]string{"add", "--name", "g1", "--targets", "test-c"})
	cfg, _ = loadConfig()
	if len(cfg.Groups["g1"]) != 3 {
		t.Error("group should have 3 after add")
	}

	// List via dispatch
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	handleGroupSubcommand([]string{"list"})
	w.Close()
	os.Stdout = old
	var buf [512]byte
	n, _ := r.Read(buf[:])
	if !strings.Contains(string(buf[:n]), "g1") {
		t.Error("should list group g1")
	}

	// Remove via dispatch
	handleGroupSubcommand([]string{"remove", "--name", "g1", "--targets", "test-c"})
	cfg, _ = loadConfig()
	if len(cfg.Groups["g1"]) != 2 {
		t.Errorf("group should have 2 after remove, got %v", cfg.Groups["g1"])
	}

	// Delete via dispatch
	handleGroupSubcommand([]string{"delete", "--name", "g1"})
	cfg, _ = loadConfig()
	if _, ok := cfg.Groups["g1"]; ok {
		t.Error("group should be deleted")
	}
}

func TestConfigMultipleTargets(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Add several targets
	for i := 0; i < 5; i++ {
		names := []string{"node-a", "node-b", "node-c", "node-d", "node-e"}
		tokens := []string{"ta", "tb", "tc", "td", "te"}
		addTarget(names[i], tokens[i])
	}

	cfg, _ := loadConfig()
	if len(cfg.Targets) != 5 {
		t.Errorf("expected 5 targets, got %d", len(cfg.Targets))
	}

	// Create groups combining them
	groupCreate("all", []string{"node-a", "node-b", "node-c", "node-d", "node-e"})
	groupCreate("half", []string{"node-a", "node-b", "node-c"})

	cfg, _ = loadConfig()
	if len(cfg.Groups["all"]) != 5 {
		t.Errorf("all group should have 5: %v", cfg.Groups["all"])
	}
	if len(cfg.Groups["half"]) != 3 {
		t.Errorf("half group should have 3: %v", cfg.Groups["half"])
	}
}

func TestResolveTargetsEdgeCases(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("web1", "t1")

	// Single target via comma list
	targets, err := resolveTargets("web1", false)
	if err != nil {
		t.Fatalf("resolveTargets: %v", err)
	}
	if len(targets) != 1 || targets[0] != "web1" {
		t.Errorf("expected [web1], got %v", targets)
	}

	// Spaces in comma list
	addTarget("web2", "t2")
	targets, err = resolveTargets("web1, web2", false)
	if err != nil {
		t.Fatalf("resolveTargets with spaces: %v", err)
	}
	if len(targets) != 2 {
		t.Errorf("expected 2 targets, got %v", targets)
	}
}

func TestSetRelayUpdatesConfig(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	err := setRelay("http://myrelay:9090", "myclient")
	if err != nil {
		t.Fatalf("setRelay: %v", err)
	}

	cfg, _ := loadConfig()
	if cfg.Relay.URL != "http://myrelay:9090" {
		t.Errorf("URL = %q", cfg.Relay.URL)
	}
	if cfg.Relay.Name != "myclient" {
		t.Errorf("Name = %q", cfg.Relay.Name)
	}
}

func TestConfigPersistence(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	// Create a full config
	setRelay("http://r:3032", "n")
	addTarget("t1", "tok1")
	addTargetWithRelayName("t2", "tok2", "t2-relay")
	groupCreate("g1", []string{"t1", "t2"})

	// Load and verify
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	// Verify file content
	data, _ := os.ReadFile(configPath())
	content := string(data)

	if !strings.Contains(content, "tok1") {
		t.Error("config should contain token")
	}
	if !strings.Contains(content, "t2-relay") {
		t.Error("config should contain relay name")
	}
	if !strings.Contains(content, "g1") {
		t.Error("config should contain group")
	}
	_ = cfg
}
