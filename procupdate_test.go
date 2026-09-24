//go:build !windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMatchesProc(t *testing.T) {
	cases := []struct {
		argv       []string
		kind, name string
		want       bool
	}{
		{[]string{"/bin/rc", "daemon", "start"}, "daemon", "", true},
		{[]string{"/bin/rc", "daemon", "start", "--token", "x"}, "daemon", "", true},
		{[]string{"/bin/rc", "daemon", "start", "-name", "dk3"}, "daemon", "dk3", true},
		{[]string{"/bin/rc", "daemon", "start", "--name=dk3"}, "daemon", "dk3", true},
		{[]string{"/bin/rc", "daemon", "start"}, "daemon", "dk3", false},
		{[]string{"/bin/rc", "daemon", "status"}, "daemon", "", false},
		{[]string{"/bin/rc", "relay", "daemon", "start", "-port", "3033"}, "relay", "", true},
		{[]string{"/bin/rc", "relay", "daemon", "start"}, "daemon", "", false},
		{[]string{"/bin/rc", "daemon", "start"}, "relay", "", false},
		{[]string{"/bin/rc"}, "daemon", "", false},
	}
	for _, c := range cases {
		if got := matchesProc(c.argv, c.kind, c.name); got != c.want {
			t.Errorf("matchesProc(%v, %s, %q) = %v, want %v", c.argv, c.kind, c.name, got, c.want)
		}
	}
}

func TestUnitFromCgroup(t *testing.T) {
	cases := []struct {
		cg, unit string
		user     bool
	}{
		{"0::/system.slice/remotecmd-daemon.service\n", "remotecmd-daemon.service", false},
		{"0::/user.slice/user-0.slice/user@0.service/app.slice/remotecmd.service\n", "remotecmd.service", true},
		{"0::/user.slice/user-0.slice/session-12.scope\n", "", false},
		{"12:pids:/system.slice/remotecmd-daemon-dk3.service\n0::/system.slice/remotecmd-daemon-dk3.service", "remotecmd-daemon-dk3.service", false},
	}
	for _, c := range cases {
		if u, user := unitFromCgroup(c.cg); u != c.unit || user != c.user {
			t.Errorf("unitFromCgroup(%q) = %q,%v want %q,%v", c.cg, u, user, c.unit, c.user)
		}
	}
}

// fakeReleaseServer serves a "binary" (a shell script passing the smoke
// test) and a checksums.txt for it; tamper corrupts the checksum.
func fakeReleaseServer(t *testing.T, version string, tamper bool) *githubRelease {
	t.Helper()
	bin := []byte("#!/bin/sh\necho \"remotecmd-cli version " + version + "\"\n")
	sum := sha256.Sum256(bin)
	digest := hex.EncodeToString(sum[:])
	if tamper {
		digest = strings.Repeat("0", 64)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			fmt.Fprintf(w, "%s  %s\n", digest, assetNameForPlatform())
			return
		}
		w.Write(bin)
	}))
	t.Cleanup(srv.Close)
	rel := &githubRelease{TagName: "v" + version}
	for _, a := range []struct{ n, u string }{{assetNameForPlatform(), srv.URL + "/bin"}, {"checksums.txt", srv.URL + "/checksums.txt"}} {
		rel.Assets = append(rel.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{a.n, a.u})
	}
	return rel
}

func TestInstallReleaseSwapsVerifiedBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "remotecmd-cli")
	os.WriteFile(exe, []byte("#!/bin/sh\necho 'remotecmd-cli version 1.0.0'\n"), 0o755)

	bak, err := installRelease(fakeReleaseServer(t, "9.9.9", false), exe)
	if err != nil {
		t.Fatalf("installRelease: %v", err)
	}
	if v := parseDaemonVersion(runQuiet(exe, "version")); v != "9.9.9" {
		t.Errorf("installed binary reports %q, want 9.9.9", v)
	}
	if v := parseDaemonVersion(runQuiet(bak, "version")); v != "1.0.0" {
		t.Errorf("backup reports %q, want 1.0.0", v)
	}
}

func TestInstallReleaseRefusesBadChecksum(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "remotecmd-cli")
	os.WriteFile(exe, []byte("#!/bin/sh\necho 'remotecmd-cli version 1.0.0'\n"), 0o755)
	if _, err := installRelease(fakeReleaseServer(t, "9.9.9", true), exe); err == nil {
		t.Fatal("expected a checksum mismatch to refuse the install")
	}
	if v := parseDaemonVersion(runQuiet(exe, "version")); v != "1.0.0" {
		t.Errorf("original binary must stay in place, reports %q", v)
	}
}

// ---- end-to-end in-place restart with real binaries ----

func buildBinary(t *testing.T, dir, version string) string {
	t.Helper()
	// Some daemon tests launch this test binary as a fake daemon
	// ("<test-binary> daemon start ..."); such a child re-runs every test.
	// Building binaries there multiplies go builds and leaves them behind.
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "-test.") {
			t.Skip("spawned as a child process, not by go test")
		}
	}
	out := filepath.Join(dir, "rc-"+version)
	cmd := exec.Command("go", "build", "-ldflags", "-X main.releaseVersion="+version, "-o", out, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func relayHealth(port int) (pid int, version string) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/_health", port))
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	var h struct {
		PID     int    `json:"pid"`
		Version string `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&h)
	return h.PID, h.Version
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A relay swapped on disk and sent SIGUSR2 comes back on the new binary
// with the same PID — what `relay daemon update` relies on under nohup.
func TestRelayRestartsInPlaceOnNewBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	dir := t.TempDir()
	v1, v2 := buildBinary(t, dir, "7.0.0-a"), buildBinary(t, dir, "7.0.0-b")
	exe := filepath.Join(dir, "remotecmd-cli")
	os.Rename(v1, exe)

	port := freePort(t)
	cmd := exec.Command(exe, "relay", "daemon", "start", "-port", fmt.Sprint(port))
	cmd.Env = append(os.Environ(), "HOME="+dir, "RCMD_CONFIG_DIR="+filepath.Join(dir, "cfg"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	waitFor(t, "relay v1", func() bool { _, v := relayHealth(port); return v == "7.0.0-a" })

	// findRunning sees it and knows it can re-exec.
	var found *runningProc
	for _, p := range findRunning("relay", "") {
		if p.PID == cmd.Process.Pid {
			p := p
			found = &p
		}
	}
	if found == nil || !found.CanReexec || found.Version != "7.0.0-a" || found.Exe != exe {
		t.Fatalf("findRunning = %+v", found)
	}

	os.Rename(v2, exe) // what installRelease does
	if r := restartProc(*found); !r.OK || r.Method != "reexec" {
		t.Fatalf("restartProc = %+v", r)
	}
	waitFor(t, "relay v2", func() bool { _, v := relayHealth(port); return v == "7.0.0-b" })
	if pid, _ := relayHealth(port); pid != cmd.Process.Pid {
		t.Errorf("PID changed %d -> %d; must re-exec in place", cmd.Process.Pid, pid)
	}
}

// A daemon asked to restart while running a command finishes that command
// (its result is delivered) before re-executing, then re-registers on the
// new binary with the same PID.
func TestDaemonRestartsInPlaceAfterInflightCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	_, port := startTestRelay(t)
	relay := fmt.Sprintf("http://127.0.0.1:%d", port)

	dir := t.TempDir()
	v1, v2 := buildBinary(t, dir, "7.0.0-a"), buildBinary(t, dir, "7.0.0-b")
	exe := filepath.Join(dir, "remotecmd-cli")
	os.Rename(v1, exe)
	env := append(os.Environ(), "HOME="+dir, "RCMD_CONFIG_DIR="+filepath.Join(dir, "cfg"))
	if out, err := (&exec.Cmd{Path: exe, Args: []string{exe, "set-relay", "--url", relay, "--name", "box"}, Env: env}).CombinedOutput(); err != nil {
		t.Fatalf("set-relay: %v %s", err, out)
	}
	d := exec.Command(exe, "daemon", "start", "--token", "tok")
	d.Env = env
	d.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Process.Kill()

	setRelay(relay, "tester")
	addTarget("box", "tok")
	runOn := func(cmdline string) string {
		return captureStdout(t, func() { handleExecWithStdin("box", cmdline, 20, false, nil) })
	}
	waitFor(t, "daemon v1", func() bool { return strings.Contains(runOn("true"), `"daemon_version": "7.0.0-a"`) })

	os.Rename(v2, exe)
	done := make(chan string, 1)
	go func() { done <- runOn("sleep 1; echo finished") }()
	time.Sleep(300 * time.Millisecond) // the command is now in flight
	if err := sendRestartSignal(d.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if out := <-done; !strings.Contains(out, "finished") {
		t.Fatalf("in-flight command lost across restart: %s", out)
	}
	waitFor(t, "daemon v2", func() bool { return strings.Contains(runOn("true"), `"daemon_version": "7.0.0-b"`) })
	if err := syscall.Kill(d.Process.Pid, 0); err != nil {
		t.Errorf("daemon PID %d gone after restart: %v", d.Process.Pid, err)
	}
}
