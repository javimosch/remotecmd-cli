package main

import (
	"fmt"
	"runtime"
	"testing"
	"time"
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
