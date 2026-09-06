//go:build !windows

package main

import (
	"context"
	"os/exec"
)

// shellCommand creates a command that runs the given script via the system shell.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}
