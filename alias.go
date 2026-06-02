package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func handleAliasSubcommand(args []string) {
	if len(args) < 1 {
		printAliasHelp()
		os.Exit(1)
	}
	switch args[0] {
	case "install":
		handleAliasInstall()
	case "uninstall":
		handleAliasUninstall()
	default:
		printAliasHelp()
		os.Exit(1)
	}
}

func handleAliasInstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}

	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create bin directory: %v\n", err)
		os.Exit(1)
	}

	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get executable path: %v\n", err)
		os.Exit(1)
	}

	// Create rc alias
	rcPath := filepath.Join(binDir, "rc")
	if err := createAliasWrapper(rcPath, execPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating rc alias: %v\n", err)
		os.Exit(1)
	}

	// Create rcx alias
	rcxPath := filepath.Join(binDir, "rcx")
	if err := createRcxWrapper(rcxPath, execPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating rcx alias: %v\n", err)
		os.Exit(1)
	}

	// Create rcl alias
	rclPath := filepath.Join(binDir, "rcl")
	if err := createRclWrapper(rclPath, execPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating rcl alias: %v\n", err)
		os.Exit(1)
	}

	// Create rcs alias
	rcsPath := filepath.Join(binDir, "rcs")
	if err := createRcsWrapper(rcsPath, execPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating rcs alias: %v\n", err)
		os.Exit(1)
	}

	// Add to shell config if needed
	shellConfig := getShellConfigPath()
	if shellConfig != "" {
		if !hasBinPathInConfig(shellConfig, binDir) {
			if err := addBinPathToConfig(shellConfig, binDir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not update shell config: %v\n", err)
				fmt.Println("Add ~/.local/bin to your PATH manually")
			} else {
				fmt.Printf("Added ~/.local/bin to PATH in %s\n", shellConfig)
				fmt.Println("Run 'source " + shellConfig + "' or restart your shell")
			}
		}
	}

	fmt.Println("Aliases installed successfully:")
	fmt.Println("  rc  - remotecmd-cli (full access)")
	fmt.Println("  rcx - execute command: rcx <target> <cmd> [timeout]")
	fmt.Println("  rcl - list targets")
	fmt.Println("  rcs - check daemon status")
}

func handleAliasUninstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}

	binDir := filepath.Join(home, ".local", "bin")
	aliases := []string{"rc", "rcx", "rcl", "rcs"}

	for _, alias := range aliases {
		path := filepath.Join(binDir, alias)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", path, err)
		}
	}

	fmt.Println("Aliases uninstalled successfully")
}

func createAliasWrapper(path, execPath string) error {
	content := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", execPath)
	return os.WriteFile(path, []byte(content), 0755)
}

func createRcxWrapper(path, execPath string) error {
	content := fmt.Sprintf(`#!/bin/sh
if [ $# -lt 2 ]; then
    echo "Usage: rcx <target> <command> [timeout]"
    echo "Example: rcx dk1 'hostname' 10"
    exit 1
fi
TARGET="$1"
CMD="$2"
TIMEOUT="${3:-10}"
exec %s --target "$TARGET" --cmd "$CMD" --timeout "$TIMEOUT"
`, execPath)
	return os.WriteFile(path, []byte(content), 0755)
}

func createRclWrapper(path, execPath string) error {
	content := fmt.Sprintf("#!/bin/sh\nexec %s list-targets\n", execPath)
	return os.WriteFile(path, []byte(content), 0755)
}

func createRcsWrapper(path, execPath string) error {
	content := fmt.Sprintf(`#!/bin/sh
if [ $# -lt 1 ]; then
    echo "Usage: rcs <target>"
    echo "Example: rcs dk1"
    exit 1
fi
TARGET="$1"
exec %s --target "$TARGET" --cmd "ps aux | grep remotecmd-cli | grep -v grep" --timeout 10
`, execPath)
	return os.WriteFile(path, []byte(content), 0755)
}

func getShellConfigPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}

	home, _ := os.UserHomeDir()

	switch {
	case shell == "/bin/zsh" || shell == "/usr/bin/zsh":
		return filepath.Join(home, ".zshrc")
	case shell == "/bin/bash" || shell == "/usr/bin/bash":
		return filepath.Join(home, ".bashrc")
	default:
		return ""
	}
}

func hasBinPathInConfig(configPath, binDir string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	content := string(data)
	return containsPath(content, binDir)
}

func containsPath(content, path string) bool {
	// Check for various PATH export patterns
	patterns := []string{
		"PATH=\"" + path,
		"PATH='" + path,
		"PATH=" + path,
		"export PATH=\"" + path,
		"export PATH='" + path,
		"export PATH=" + path,
	}
	for _, pattern := range patterns {
		if contains(content, pattern) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func addBinPathToConfig(configPath, binDir string) error {
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	line := fmt.Sprintf("\n# remotecmd-cli aliases\nexport PATH=\"%s:$PATH\"\n", binDir)
	_, err = f.WriteString(line)
	return err
}

func printAliasHelp() {
	fmt.Println(`Usage: remotecmd-cli alias <command>

Commands:
  install    Install convenience aliases (rc, rcx, rcl, rcs)
  uninstall  Remove installed aliases`)
}