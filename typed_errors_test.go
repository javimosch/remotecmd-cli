package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Every command group, called with no subcommand or an unknown one, must
// leave stdout empty and emit exactly one typed error whose code is the
// exit status (cli-output-spec §1-§3).
func TestUsageErrorsAreTypedAndOffStdout(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	cases := [][]string{
		{"relay"}, {"relay", "bogus"},
		{"relay", "daemon"}, {"relay", "daemon", "bogus"},
		{"relay", "add-key"}, {"relay", "remove-key"},
		{"daemon"}, {"daemon", "bogus"},
		{"group"}, {"group", "bogus"}, {"group", "create"}, {"group", "delete"},
		{"alias"}, {"alias", "bogus"},
		{"pair"}, {"pair", "bogus"}, {"pair", "accept"}, {"pair", "disconnect"},
		{"sidecar"}, {"sidecar", "bogus"}, {"sidecar", "activate"},
		{"tunnel", "start"}, {"tunnel", "stop"},
		{"cp"}, {"feedback"}, {"update", "--bogus"},
	}
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	for _, args := range cases {
		name := strings.Join(args, " ")
		os.Args = append([]string{"remotecmd-cli"}, args...)
		var code int
		var body map[string]any
		stdout := captureStdout(t, func() { code, body = captureError(t, main) })
		if stdout != "" {
			t.Errorf("%s: wrote to stdout: %q", name, stdout)
		}
		e, _ := body["error"].(map[string]any)
		if e == nil {
			t.Errorf("%s: no typed error: %v", name, body)
			continue
		}
		if int(e["code"].(float64)) != code || code != ExitConfigError {
			t.Errorf("%s: exit %d, error.code %v; want both %d", name, code, e["code"], ExitConfigError)
		}
		if typ, _ := e["type"].(string); typ == "" || e["recoverable"] != false {
			t.Errorf("%s: error = %v", name, e)
		}
		if s, _ := e["suggestions"].([]any); len(s) == 0 {
			t.Errorf("%s: no suggestions", name)
		}
	}
}

// Humans still get the usage text (on stderr) before the error line.
func TestFailUsageTextMode(t *testing.T) {
	t.Setenv("RCMD_ERROR_FORMAT", "text")
	var buf bytes.Buffer
	oldOut, oldExit := errorOut, osExit
	errorOut = &buf
	osExit = func(code int) { panic(exitCodePanic(code)) }
	defer func() { errorOut, osExit = oldOut, oldExit }()

	stdout := captureStdout(t, func() {
		defer func() { recover() }()
		failUsage("missing_argument", "group needs a subcommand", printGroupHelp)
	})
	if stdout != "" {
		t.Errorf("usage leaked to stdout: %q", stdout)
	}
	out := buf.String()
	if !strings.Contains(out, "Usage: remotecmd-cli group") || !strings.HasSuffix(out, "Error: group needs a subcommand\n") {
		t.Errorf("text usage error = %q", out)
	}
}

// An explicit help request still prints to stdout.
func TestHelpRequestStillOnStdout(t *testing.T) {
	if out := captureStdout(t, printGroupHelp); !strings.Contains(out, "Usage: remotecmd-cli group") {
		t.Errorf("printGroupHelp stdout = %q", out)
	}
}

func TestUpdateFailuresAreRecoverable(t *testing.T) {
	code, body := captureError(t, func() { failErrCode(exitUpdateFail, errString("download failed: timeout")) })
	e := body["error"].(map[string]any)
	if code != exitUpdateFail || e["type"] != "update_failed" || e["recoverable"] != true {
		t.Errorf("exit %d error %v; want 100 update_failed recoverable", code, e)
	}
}
