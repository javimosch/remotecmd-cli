package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func handleSidecarSubcommand(args []string) {
	if len(args) < 1 {
		printSidecarHelp()
		osExit(ExitConfigError)
	}
	switch args[0] {
	case "activate":
		handleSidecarActivate(args[1:])
	default:
		printSidecarHelp()
		osExit(ExitConfigError)
	}
}

// handleSidecarActivate POSTs a pair request to a remotecmd sidecar running
// inside a remote container. The sidecar downloads the rcmd binary, accepts
// the pair code, and starts the daemon — all in-process, no terminal needed.
func handleSidecarActivate(args []string) {
	fs := flag.NewFlagSet("sidecar activate", flag.ExitOnError)
	url := fs.String("url", "", "sidecar endpoint URL (e.g. https://app.coolify.app/__rcmd/pair)")
	relayURL := fs.String("relay", "", "relay URL the sidecar should connect to (e.g. http://92.113.145.178:3032)")
	code := fs.String("code", "", "pair code (from 'pair listen')")
	activationKey := fs.String("activation-key", "", "activation key (required if listener used --require-activation-key)")
	name := fs.String("name", "", "optional node name for the target")
	endpointPath := fs.String("path", "/__rcmd/pair", "sidecar endpoint path (default: /__rcmd/pair)")
	timeoutSec := fs.Int("timeout", 60, "HTTP request timeout in seconds")
	fs.Parse(args)

	if *url == "" || *relayURL == "" || *code == "" {
		fmt.Fprintln(os.Stderr, "Error: --url, --relay, and --code are required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli sidecar activate --url <u> --relay <r> --code <c> [--activation-key <k>] [--name <n>]")
		osExit(ExitConfigError)
	}

	// Build the full URL if --url is just the base
	fullURL := *url
	if *endpointPath != "" {
		// If url already ends with the path, don't double it
		if !endsWith(fullURL, *endpointPath) {
			fullURL = trimSuffix(fullURL, "/") + *endpointPath
		}
	}

	payload := map[string]string{
		"relayUrl": *relayURL,
		"code":     *code,
	}
	if *activationKey != "" {
		payload["activationKey"] = *activationKey
	}
	if *name != "" {
		payload["name"] = *name
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building request: %v\n", err)
		osExit(ExitConfigError)
	}

	fmt.Printf("Activating sidecar at %s...\n", fullURL)

	client := &http.Client{Timeout: time.Duration(*timeoutSec) * time.Second}
	resp, err := client.Post(fullURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Sidecar returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		osExit(ExitConfigError)
	}

	fmt.Printf("Sidecar activated: %s\n", string(respBody))
	fmt.Println("Waiting for pair to complete on the listen side...")
}

func printSidecarHelp() {
	fmt.Println(`Usage: remotecmd-cli sidecar <command>

Commands:
  activate --url <u> --relay <r> --code <c> [--activation-key <k>] [--name <n>] [--path <p>]
      POST a pair request to a sidecar running in a remote container.
      The sidecar downloads rcmd, accepts the pair code, and starts the daemon.

  --url       Sidecar base URL or full endpoint (e.g. https://app.coolify.app)
  --relay     Relay URL for the daemon to connect to
  --code      Pair code (from 'pair listen')
  --activation-key  Activation key (if listener used --require-activation-key)
  --name      Optional node name for the target
  --path      Sidecar endpoint path (default: /__rcmd/pair)
  --timeout   HTTP request timeout in seconds (default: 60)`)
}

func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

func trimSuffix(s, suffix string) string {
	if endsWith(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}
