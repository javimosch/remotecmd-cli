package main

import (
	"errors"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil error", nil, ExitSuccess},
		{"connect to relay", errors.New("connect to relay: dial tcp ... connection refused"), ExitRelayError},
		{"connection timeout", errors.New("connection timed out"), ExitRelayError},
		{"timed out waiting", errors.New("timed out waiting for response"), ExitRelayError},
		{"unknown target", errors.New(`unknown target "web99"`), ExitConfigError},
		{"target not found", errors.New("target not found"), ExitConfigError},
		{"required flag", errors.New("--target and --cmd are required"), ExitConfigError},
		{"already exists", errors.New(`group "web" already exists`), ExitConfigError},
		{"not configured", errors.New("relay not configured"), ExitConfigError},
		{"unknown command", errors.New("unknown command: foo"), ExitConfigError},
		{"execution error", errors.New("command failed with exit code 1"), ExitExecError},
		{"generic error", errors.New("something went wrong"), ExitExecError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.want {
				t.Errorf("classifyError(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestMultiExecExit(t *testing.T) {
	tests := []struct {
		name   string
		result *Message
		want   int
	}{
		{"nil result", nil, ExitRelayError},
		{"nil results map", &Message{}, ExitRelayError},
		{"all ok", &Message{
			Results: map[string]*Message{
				"web1": {OK: boolPtr(true), ExitCode: 0},
			},
		}, ExitSuccess},
		{"with error string", &Message{
			Results: map[string]*Message{
				"web1": {OK: boolPtr(false), Error: "target not connected"},
			},
		}, ExitExecError},
		{"with non-zero exit", &Message{
			Results: map[string]*Message{
				"web1": {OK: boolPtr(true), ExitCode: 1},
			},
		}, ExitExecError},
		{"mixed results", &Message{
			Results: map[string]*Message{
				"web1": {OK: boolPtr(true), ExitCode: 0},
				"web2": {OK: boolPtr(false), Error: "failed"},
			},
		}, ExitExecError},
		{"all success", &Message{
			Results: map[string]*Message{
				"web1": {OK: boolPtr(true), ExitCode: 0},
				"web2": {OK: boolPtr(true), ExitCode: 0},
			},
		}, ExitSuccess},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := multiExecExit(tt.result)
			if got != tt.want {
				t.Errorf("multiExecExit(%v) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}
