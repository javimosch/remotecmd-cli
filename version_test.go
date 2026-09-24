package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestResolveVersion(t *testing.T) {
	none := func() (string, bool) { return "", false }
	clean := func() (string, bool) { return "210eb7fe5ca0a1b2", false }
	dirty := func() (string, bool) { return "210eb7fe5ca0a1b2", true }

	cases := []struct {
		release string
		vcs     func() (string, bool)
		want    string
	}{
		{"2.6.0", dirty, "2.6.0"}, // -ldflags -X wins
		{"", clean, nextVersion + "-dev+210eb7f"},
		{"", dirty, nextVersion + "-dev+210eb7f.dirty"},
		{"", none, nextVersion + "-dev"}, // built outside git
	}
	for _, c := range cases {
		if got := resolveVersion(c.release, c.vcs); got != c.want {
			t.Errorf("resolveVersion(%q) = %q, want %q", c.release, got, c.want)
		}
	}
}

func TestVersionLessPrerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.6.0-dev+210eb7f", "2.6.0", true}, // dev sorts before its release
		{"2.6.0", "2.6.0-dev+210eb7f", false},
		{"2.5.0", "2.6.0-dev+abc", true},  // dev is ahead of the previous release
		{"2.6.0-dev+abc", "2.5.0", false}, // so update must not "downgrade" it
		{"2.6.0-dev+abc", "2.6.0-dev+def", false},
		{"2.6.0+build", "2.6.0", false}, // build metadata is not a pre-release
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A normal exec against a self-reporting daemon refreshes the cached
// version, so an upgrade shows up without waiting for a health probe.
func TestExecRecordsReportedDaemonVersion(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	_, port := startTestRelay(t)
	relay := fmt.Sprintf("http://127.0.0.1:%d", port)
	setRelay(relay, "tester")
	d := testDaemon(t, relay, "box", "tok")
	defer d.Close()
	fakeDaemon(t, d, "2.6.0", true)
	addTarget("box", "tok")
	saveHealthCache(&HealthCache{Targets: map[string]TargetHealth{"box": {Status: "up", Version: "1.0.0"}}})
	time.Sleep(50 * time.Millisecond)

	captureStdout(t, func() {
		if err := handleExecWithStdin("box", "hostname", 5, false, nil); err != nil {
			t.Errorf("exec: %v", err)
		}
	})
	if v := knownDaemonVersion("box"); v != "2.6.0" {
		t.Errorf("cached version = %q, want 2.6.0 after exec", v)
	}
}

func TestNoteDaemonVersionIgnoresEmpty(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	saveHealthCache(&HealthCache{Targets: map[string]TargetHealth{"box": {Version: "2.4.1"}}})
	noteDaemonVersion("box", "") // old daemons report nothing: keep what we know
	if v := knownDaemonVersion("box"); v != "2.4.1" {
		t.Errorf("version = %q, want 2.4.1 kept", v)
	}
}

func TestDaemonFileTransferResultsCarryVersion(t *testing.T) {
	if m := stampVersion(&Message{Type: "file_transfer_result"}); m.DaemonVersion != Version || !strings.Contains(m.Type, "file_transfer") {
		t.Errorf("stampVersion = %+v", m)
	}
}
