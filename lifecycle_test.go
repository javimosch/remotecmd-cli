package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNamedDaemonPidFile(t *testing.T) {
	if namedDaemonPidFile("") != daemonPidFile {
		t.Errorf("default instance must keep the legacy PID file")
	}
	if got := namedDaemonPidFile("dk3"); got != daemonPidFile+"-dk3" || got == namedDaemonPidFile("other") {
		t.Errorf("named PID files must be distinct per name, got %q", got)
	}
}

// status --json follows cli-daemon-spec: exit 0 running, 3 stopped.
func TestReportStatusJSON(t *testing.T) {
	dir := t.TempDir()

	running := filepath.Join(dir, "running.pid")
	os.WriteFile(running, []byte(fmt.Sprint(os.Getpid())), 0o644)
	var out string
	assertExitCodeSuccess(t, func() {
		out = captureStdout(t, func() { reportStatus(running, "dk3", true) })
	})
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("status --json not JSON: %q", out)
	}
	if body["ok"] != true || body["daemon"] != "running" || int(body["pid"].(float64)) != os.Getpid() || body["name"] != "dk3" {
		t.Errorf("running status = %v", body)
	}

	stopped := filepath.Join(dir, "none.pid")
	assertExitCode(t, ExitConfigError, func() {
		out = captureStdout(t, func() { reportStatus(stopped, "", true) })
	})
}

func TestRelayHealthEndpoints(t *testing.T) {
	srv := httptest.NewServer(NewRelayServer().mux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/_health")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 200 || body["ok"] != true || body["service"] != "remotecmd-relay" || int(body["pid"].(float64)) != os.Getpid() {
		t.Errorf("/_health = %d %v", resp.StatusCode, body)
	}

	resp, err = http.Get(srv.URL + "/health") // legacy probe keeps working
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/health = %d", resp.StatusCode)
	}
}

func TestRelayListenAddr(t *testing.T) {
	cases := map[string]string{"": ":3032", "127.0.0.1": "127.0.0.1:3032", "::1": "[::1]:3032"}
	for host, want := range cases {
		if got := relayListenAddr(host, 3032); got != want {
			t.Errorf("relayListenAddr(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestRelayServeOnHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	rs := NewRelayServer()
	go rs.ServeOn("127.0.0.1", port)
	for i := 0; i < 50; i++ {
		var resp *http.Response
		if resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/_health", port)); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("relay bound to 127.0.0.1 not reachable there: %v", err)
}

// A relay that stops answering pings (half-dead link) must make the daemon
// give up and return from run() so it reconnects.
func TestDaemonKeepaliveDetectsSilentRelay(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	oldP, oldW := daemonPingPeriod, daemonPongWait
	daemonPingPeriod, daemonPongWait = 50*time.Millisecond, 250*time.Millisecond
	defer func() { daemonPingPeriod, daemonPongWait = oldP, oldW }()

	release := make(chan struct{})
	defer close(release)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var reg Message
		c.ReadJSON(&reg)
		c.WriteJSON(&Message{Type: "registered", Name: reg.Name})
		<-release // stop reading: pings go unanswered
	}))
	defer srv.Close()

	td := &TargetDaemon{relayURL: "ws" + strings.TrimPrefix(srv.URL, "http"), name: "quiet", token: "tok"}
	done := make(chan struct{})
	start := time.Now()
	go func() { td.run(); close(done) }()
	select {
	case <-done:
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("daemon took %v to notice the silent relay", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon never noticed the relay stopped answering pings")
	}
}
