package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

func TestParseDaemonVersion(t *testing.T) {
	cases := map[string]string{
		"remotecmd-cli version 2.5.0\n": "2.5.0",
		"rcmd version 1.0.0-cloud":      "1.0.0-cloud",
		"remotecmd-cli version v1.8.0":  "v1.8.0",
		"sh: /proc/1/exe: not found":    "",
		"'/proc/$PPID/exe' is not recognized as an internal or external command": "",
		"": "",
	}
	for in, want := range cases {
		if got := parseDaemonVersion(in); got != want {
			t.Errorf("parseDaemonVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.0", "1.8.0", true},
		{"1.8.0", "1.8.0", false},
		{"2.4.1", "2.5.0", true},
		{"2.10.0", "2.9.0", false},
		{"1.0.0-cloud", "1.8.0", true},
		{"v2.5.0", "2.5.0", false},
		{"", "1.8.0", false},        // unknown never warns
		{"garbage", "1.8.0", false}, // unparseable never warns
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDaemonResultsCarryVersion(t *testing.T) {
	if okResult("i", "", "", 0, 1).DaemonVersion != Version ||
		streamEndOK("i", 0, 1).DaemonVersion != Version ||
		streamEndErr("i", "x").DaemonVersion != Version {
		t.Error("daemon-built results must carry DaemonVersion")
	}
	// errResult is also built by the relay; it must not claim a daemon version.
	if errResult("i", "target not connected").DaemonVersion != "" {
		t.Error("errResult must not carry DaemonVersion")
	}
}

func TestWarnIfDaemonTooOld(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	saveHealthCache(&HealthCache{Targets: map[string]TargetHealth{
		"old": {Status: "up", Version: "1.0.0"},
		"new": {Status: "up", Version: "2.5.0"},
	}})
	var buf bytes.Buffer
	old := warnOut
	warnOut = &buf
	defer func() { warnOut = old }()

	warnIfDaemonTooOld("new", "stdin", "stdin is ignored")
	warnIfDaemonTooOld("unknown-target", "stdin", "stdin is ignored")
	if buf.Len() != 0 {
		t.Fatalf("unexpected warning: %q", buf.String())
	}
	warnIfDaemonTooOld("old", "stdin", "stdin is ignored")
	if !strings.Contains(buf.String(), "old runs daemon v1.0.0; stdin needs v1.8.0") {
		t.Errorf("warning = %q", buf.String())
	}
}

// fakeDaemon answers commands like a daemon of the given version would.
// reportVersion=false mimics a daemon (or relay path) without daemon_version,
// so the client must fall back to the /proc probe.
func fakeDaemon(t *testing.T, conn *websocket.Conn, version string, reportVersion bool) *[]string {
	t.Helper()
	var seen []string
	go func() {
		for {
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type != "command" {
				continue
			}
			seen = append(seen, msg.Cmd)
			out := "fakehost\n"
			if msg.Cmd == daemonVersionProbe {
				out = fmt.Sprintf("remotecmd-cli version %s\n", version)
			}
			r := &Message{Type: "result", ID: msg.ID, OK: boolPtr(true), Stdout: out}
			if reportVersion {
				r.DaemonVersion = version
			}
			conn.WriteJSON(r)
		}
	}()
	return &seen
}

func TestPingTargetsRecordsDaemonVersions(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	_, port := startTestRelay(t)
	relay := fmt.Sprintf("http://127.0.0.1:%d", port)
	setRelay(relay, "tester")

	oldD := testDaemon(t, relay, "olddaemon", "tok-old")
	defer oldD.Close()
	oldSeen := fakeDaemon(t, oldD, "1.0.0", false)
	newD := testDaemon(t, relay, "newdaemon", "tok-new")
	defer newD.Close()
	newSeen := fakeDaemon(t, newD, "2.6.0", true)
	addTarget("olddaemon", "tok-old")
	addTarget("newdaemon", "tok-new")
	time.Sleep(50 * time.Millisecond)

	res := pingTargets([]string{"olddaemon", "newdaemon"})
	if got := res["olddaemon"]; got.Status != "up" || got.Version != "1.0.0" {
		t.Errorf("olddaemon = %+v, want up with probed version 1.0.0", got)
	}
	if got := res["newdaemon"]; got.Status != "up" || got.Version != "2.6.0" {
		t.Errorf("newdaemon = %+v, want up with reported version 2.6.0", got)
	}
	if len(*newSeen) != 1 {
		t.Errorf("self-reporting daemon should not be probed again, got commands %v", *newSeen)
	}
	if len(*oldSeen) != 2 || (*oldSeen)[1] != daemonVersionProbe {
		t.Errorf("old daemon should get hostname then the version probe, got %v", *oldSeen)
	}
}

func TestListTargetsKeepsVersionWhileDown(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	addTarget("gone", "tok") // no relay configured: the probe marks it down
	saveHealthCache(&HealthCache{Targets: map[string]TargetHealth{
		"gone": {Status: "up", Version: "1.0.0"},
	}})
	captureStdout(t, func() { listTargetsSmart(true, false, true) })
	c, _ := loadHealthCache()
	if h := c.Targets["gone"]; h.Status != "down" || h.Version != "1.0.0" {
		t.Errorf("cache = %+v, want down with version 1.0.0 kept", h)
	}
}

func TestPrintAlignedTableColumnsLineUp(t *testing.T) {
	out := captureStdout(t, func() {
		printAlignedTable([][]string{
			{"TARGET", "STATUS", "VERSION", "HOSTNAME"},
			{"mikavm3 → PRINTER-BOT-V1", "up", "2.6.0-dev+2bc1968", "PRINTER-BOT-V1"},
			{"dk1", "up", "2.4.1*", "vpspoly1"},
		})
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "---") {
		t.Fatalf("table = %q", out)
	}
	// The last column starts at the same rune offset on every row.
	col := func(l, cell string) int { return utf8.RuneCountInString(l[:strings.LastIndex(l, cell)]) }
	if a, b, c := col(lines[0], "HOSTNAME"), col(lines[2], "PRINTER-BOT-V1"), col(lines[3], "vpspoly1"); a != b || b != c {
		t.Errorf("HOSTNAME column misaligned: %d %d %d\n%s", a, b, c, out)
	}
}
