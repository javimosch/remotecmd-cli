package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A daemon replaced on its live connection by another process with the same
// name and token (a duplicate) exits instead of idling unrouted.
func TestDaemonExitsWhenReplacedByDuplicate(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	_, port := startTestRelay(t)
	relay := fmt.Sprintf("http://127.0.0.1:%d", port)

	exited := make(chan int, 1)
	old := osExit
	osExit = func(code int) { exited <- code; runtime.Goexit() }
	defer func() { osExit = old }()

	td := &TargetDaemon{relayURL: wsURL(relay), name: "dup", token: "tok"}
	go td.run()
	time.Sleep(200 * time.Millisecond) // let it register

	dup := testDaemon(t, relay, "dup", "tok") // same name, same token
	defer dup.Close()

	select {
	case code := <-exited:
		if code != ExitConfigError {
			t.Errorf("exit code = %d, want %d", code, ExitConfigError)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replaced daemon did not exit")
	}
}

// The daemon announces its build when it registers, so relay logs show it.
func TestDaemonRegisterCarriesVersion(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	got := make(chan Message, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var reg Message
		c.ReadJSON(&reg)
		got <- reg
	}))
	defer srv.Close()

	td := &TargetDaemon{relayURL: "ws" + strings.TrimPrefix(srv.URL, "http"), name: "v", token: "tok"}
	go td.run()
	select {
	case reg := <-got:
		if reg.Type != "register" || reg.DaemonVersion != Version {
			t.Errorf("register = %+v, want daemon_version %q", reg, Version)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon never registered")
	}
	if registeredVersion("") == "" || registeredVersion("2.6.0") != "v2.6.0" {
		t.Error("registeredVersion labels")
	}
}
