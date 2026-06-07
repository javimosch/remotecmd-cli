package main

import "strings"

// Exit codes for script-friendly error handling.
// Scripts can check these codes to determine what went wrong:
//   exit 0 = all good
//   exit 1 = one or more targets returned errors
//   exit 2 = can't connect to relay
//   exit 3 = bad arguments / missing config
//   exit 4 = unexpected internal error
const (
	ExitSuccess     = 0
	ExitExecError   = 1
	ExitRelayError  = 2
	ExitConfigError = 3
	ExitInternal    = 4
)

// classifyError maps common error messages to exit codes.
func classifyError(err error) int {
	if err == nil {
		return ExitSuccess
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connect to relay"),
		strings.Contains(msg, "connection"),
		strings.Contains(msg, "timed out waiting"):
		return ExitRelayError
	case strings.Contains(msg, "unknown target"),
		strings.Contains(msg, "not found"),
		strings.Contains(msg, "required"),
		strings.Contains(msg, "already exists"),
		strings.Contains(msg, "not configured"),
		strings.Contains(msg, "unknown command"):
		return ExitConfigError
	default:
		return ExitExecError
	}
}

// multiExecExit determines exit code from a multi-target result.
func multiExecExit(result *Message) int {
	if result == nil || result.Results == nil {
		return ExitRelayError
	}
	hasFailure := false
	for _, r := range result.Results {
		if r.Error != "" || (r.OK != nil && !*r.OK) {
			hasFailure = true
			break
		}
		if r.ExitCode != 0 {
			hasFailure = true
			break
		}
	}
	if hasFailure {
		return ExitExecError
	}
	return ExitSuccess
}
