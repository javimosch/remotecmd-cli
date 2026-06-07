package main

import (
	"os"
	"strings"
	"testing"
)

func TestAddTargetEdgeDuplicate(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	err := addTarget("web1", "token1")
	if err != nil {
		t.Fatalf("addTarget first: %v", err)
	}

	// Adding same name should update (not error)
	err = addTarget("web1", "token2")
	if err != nil {
		t.Fatalf("addTarget duplicate should update: %v", err)
	}

	cfg, _ := loadConfig()
	if cfg.Targets["web1"].Token != "token2" {
		t.Errorf("token should be updated: %s", cfg.Targets["web1"].Token)
	}
}

func TestAddTargetWithRelayNameEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	err := addTargetWithRelayName("web1", "t1", "")
	if err != nil {
		t.Fatalf("addTargetWithRelayName: %v", err)
	}

	cfg, _ := loadConfig()
	if cfg.Targets["web1"].RelayName != "" {
		t.Errorf("RelayName should be empty: %s", cfg.Targets["web1"].RelayName)
	}
}

func TestListTargetsWithRelayName(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTargetWithRelayName("web1", "abcdef123456", "web1-relay")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	listTargets()

	w.Close()
	os.Stdout = old

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "web1-relay") {
		t.Errorf("should show relay name: %s", output)
	}
	if !strings.Contains(output, "abcd...") {
		t.Errorf("should show truncated token: %s", output)
	}
}

func TestContainsFunction(t *testing.T) {
	tests := []struct {
		s, sub string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello world", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "a", false},
		{"same", "same", true},
	}
	for _, tt := range tests {
		got := contains(tt.s, tt.sub)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.sub, got, tt.want)
		}
	}
}

func TestContainsPathVariants(t *testing.T) {
	tests := []struct {
		content string
		path    string
		want    bool
	}{
		{`export PATH="/home/user/bin:$PATH"`, "/home/user/bin", true},
		{`export PATH='/home/user/bin:$PATH'`, "/home/user/bin", true},
		{`export PATH=/home/user/bin:$PATH`, "/home/user/bin", true},
		{`PATH="/home/user/bin:$PATH"`, "/home/user/bin", true},
		{`PATH='/home/user/bin:$PATH'`, "/home/user/bin", true},
		{`PATH=/home/user/bin:$PATH`, "/home/user/bin", true},
		{`  export PATH="/home/user/bin:$PATH"`, "/home/user/bin", true},
		{``, "/any/path", false},
	}
	for _, tt := range tests {
		got := containsPath(tt.content, tt.path)
		if got != tt.want {
			t.Errorf("containsPath(%q, %q) = %v, want %v", tt.content, tt.path, got, tt.want)
		}
	}
}
