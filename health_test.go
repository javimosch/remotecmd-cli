package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthCacheLoadSaveRoundTrip(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	c := &HealthCache{Targets: map[string]TargetHealth{
		"prod": {Status: "up", Hostname: "prod", LastSeen: time.Now(), LastCheck: time.Now()},
	}}
	if err := saveHealthCache(c); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadHealthCache()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Targets["prod"].Status != "up" {
		t.Errorf("expected up, got %q", loaded.Targets["prod"].Status)
	}
	if loaded.Targets["prod"].Hostname != "prod" {
		t.Errorf("expected hostname prod, got %q", loaded.Targets["prod"].Hostname)
	}
}

func TestLoadHealthCacheMissingFile(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	c, err := loadHealthCache()
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if c.Targets == nil {
		t.Error("expected non-nil Targets map for missing cache")
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "just now"},
		{45 * time.Second, "45s"},
		{5 * time.Minute, "5min"},
		{3 * time.Hour, "3h"},
		{48 * time.Hour, "2d"},
	}
	for _, c := range cases {
		got := humanDuration(c.d)
		if got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestListTargetsSmartNoHealthShowsTargetsAndGroups(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("box-a", "tok-a")
	addTarget("box-b", "tok-b")
	groupCreate("web", []string{"box-a", "box-b"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// --no-health routes to the legacy headerless listTargets() view, so we
	// expect the target names and the Groups section, but NOT a STATUS column.
	if err := listTargetsSmart(false, true, false); err != nil {
		t.Fatalf("listTargetsSmart: %v", err)
	}

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	for _, want := range []string{"box-a", "box-b", "Groups:", "web"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output:\n%s", want, output)
		}
	}
	if strings.Contains(output, "STATUS") {
		t.Errorf("legacy --no-health view should not print health table header:\n%s", output)
	}
}

func TestListTargetsSmartJSON(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("box-a", "tok-a")
	groupCreate("web", []string{"box-a"})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := listTargetsSmart(false, true, true); err != nil {
		t.Fatalf("listTargetsSmart json: %v", err)
	}

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := strings.TrimSpace(string(buf[:n]))

	var parsed struct {
		Targets []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"targets"`
		Groups map[string][]string `json:"groups"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, output)
	}
	if len(parsed.Targets) != 1 || parsed.Targets[0].Name != "box-a" {
		t.Errorf("expected one target box-a, got %+v", parsed.Targets)
	}
	if parsed.Targets[0].Status != "unknown" {
		t.Errorf("expected unknown status without probe, got %q", parsed.Targets[0].Status)
	}
	if _, ok := parsed.Groups["web"]; !ok {
		t.Errorf("expected group web in JSON, got %+v", parsed.Groups)
	}
}

func TestListTargetsSmartEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := listTargetsSmart(false, true, false); err != nil {
		t.Fatalf("listTargetsSmart empty: %v", err)
	}

	w.Close()
	os.Stdout = old

	var buf [256]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "No targets") {
		t.Errorf("expected 'No targets', got: %s", output)
	}
}

func TestListTargetsSmartUsesCachedHealthWhenFresh(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("cached-box", "tok")

	// Seed a fresh cache entry (LastCheck = now) so no probe should happen.
	cache := &HealthCache{Targets: map[string]TargetHealth{
		"cached-box": {Status: "up", Hostname: "cached-host", LastSeen: time.Now(), LastCheck: time.Now()},
	}}
	if err := saveHealthCache(cache); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// noHealth=false but cache is fresh -> should NOT probe, should show cached "up".
	if err := listTargetsSmart(false, false, false); err != nil {
		t.Fatalf("listTargetsSmart: %v", err)
	}

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "cached-box") {
		t.Errorf("expected target name: %s", output)
	}
	if !strings.Contains(output, "up") {
		t.Errorf("expected cached 'up' status (no probe): %s", output)
	}
	if !strings.Contains(output, "cached-host") {
		t.Errorf("expected cached hostname: %s", output)
	}
}

func TestPingTargetsNoRelayReturnsDown(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()

	addTarget("lonely", "tok")
	// No relay configured -> pingTargets should mark target down, not panic.

	results := pingTargets([]string{"lonely"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	h, ok := results["lonely"]
	if !ok {
		t.Fatal("expected result for lonely")
	}
	if h.Status != "down" {
		t.Errorf("expected down (no relay), got %q", h.Status)
	}
	if h.Error == "" {
		t.Error("expected non-empty error for down target")
	}
}

func TestHealthCachePathIsolated(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-health-path-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	p := healthCachePath()
	if !strings.HasSuffix(p, filepath.Join(".remotecmd", "health.json")) {
		t.Errorf("unexpected health cache path: %s", p)
	}
}
