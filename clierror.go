package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Typed errors (cli-output-spec §3). Every failure the CLI itself reports
// goes through fail/failErr so an agent gets one parseable body:
//
//	{"ok":false,"error":{"code":3,"type":"unknown_target","message":"…",
//	  "recoverable":false,"suggestions":["remotecmd-cli list-targets"]}}
//
// The body goes to stderr (stdout stays data-only). It is JSON when stderr is
// not a terminal — agents, pipes, CI — and the familiar "Error: …" text when
// a human is watching. RCMD_ERROR_FORMAT=json|text forces either.
//
// error.code is always the process exit status. The codes are still the
// legacy 0-4 set; moving to the spec's 80-119 ranges is a separate, breaking
// change.

// cliError is the error body of cli-output-spec §3.
type cliError struct {
	Code        int      `json:"code"`
	Type        string   `json:"type"`
	Message     string   `json:"message"`
	Recoverable bool     `json:"recoverable"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// errorOut is where error bodies are written; tests swap it.
var errorOut io.Writer = os.Stderr

// fail reports a typed error and exits with code.
func fail(code int, typ, msg string, suggestions ...string) {
	writeError(errorOut, cliError{
		Code:        code,
		Type:        typ,
		Message:     msg,
		Recoverable: code == ExitRelayError,
		Suggestions: suggestions,
	})
	osExit(code)
}

// failErr reports err with the exit code and type derived from its message.
func failErr(err error, suggestions ...string) {
	failErrCode(classifyError(err), err, suggestions...)
}

// failErrCode is failErr with an explicit exit code.
func failErrCode(code int, err error, suggestions ...string) {
	typ := errorType(code, err.Error())
	if len(suggestions) == 0 {
		suggestions = defaultSuggestions[typ]
	}
	fail(code, typ, err.Error(), suggestions...)
}

// defaultSuggestions are the next steps offered when a call site has none.
var defaultSuggestions = map[string][]string{
	"unknown_target":    {"remotecmd-cli list-targets", "remotecmd-cli add-target --name <n> --token <t>"},
	"relay_unreachable": {"retry with backoff", "remotecmd-cli list-targets --refresh"},
	"not_configured":    {"remotecmd-cli set-relay --url <u> --name <n>"},
}

func writeError(w io.Writer, e cliError) {
	if errorFormatJSON() {
		b, _ := json.Marshal(struct {
			OK    bool     `json:"ok"`
			Error cliError `json:"error"`
		}{false, e})
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "Error: %s\n", e.Message)
	for _, s := range e.Suggestions {
		fmt.Fprintf(w, "  try: %s\n", s)
	}
}

func errorFormatJSON() bool {
	switch strings.ToLower(os.Getenv("RCMD_ERROR_FORMAT")) {
	case "json":
		return true
	case "text":
		return false
	}
	fi, err := os.Stderr.Stat()
	if err != nil {
		return true
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// errorType maps an exit code and message to a stable snake_case type.
// Agents branch on the type; the message is free to change.
func errorType(code int, msg string) string {
	switch code {
	case ExitRelayError:
		return "relay_unreachable"
	case ExitInternal:
		return "internal"
	case ExitExecError:
		return "command_failed"
	}
	switch {
	case strings.Contains(msg, "unknown target"):
		return "unknown_target"
	case strings.Contains(msg, "unknown command"), strings.Contains(msg, "Unknown command"):
		return "unknown_command"
	case strings.Contains(msg, "not configured"):
		return "not_configured"
	case strings.Contains(msg, "required"):
		return "missing_argument"
	case strings.Contains(msg, "already exists"):
		return "already_exists"
	case strings.Contains(msg, "not found"):
		return "not_found"
	}
	return "invalid_arguments"
}
