package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func handleAddTarget(args []string) {
	fs := flag.NewFlagSet("add-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	token := fs.String("token", "", "auth token")
	fs.Parse(args)

	if *name == "" || *token == "" {
		fail(ExitConfigError, "missing_argument", "--name and --token are required",
			"remotecmd-cli add-target --name <n> --token <t>")
	}

	if err := addTarget(*name, *token); err != nil {
		failErrCode(ExitInternal, err)
	}
	fmt.Printf("Target %q added\n", *name)
}

func handleRemoveTarget(args []string) {
	fs := flag.NewFlagSet("remove-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	fs.Parse(args)

	if *name == "" {
		fail(ExitConfigError, "missing_argument", "--name is required",
			"remotecmd-cli remove-target --name <n>")
	}

	if err := removeTarget(*name); err != nil {
		failErrCode(ExitInternal, err)
	}
	fmt.Printf("Target %q removed\n", *name)
}

func handleListTargets(args []string) {
	fs := flag.NewFlagSet("list-targets", flag.ExitOnError)
	refresh := fs.Bool("refresh", false, "force a fresh health probe of every target")
	noHealth := fs.Bool("no-health", false, "skip health probing; show config-only view (legacy)")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	fs.Parse(args)

	if err := listTargetsSmart(*refresh, *noHealth, *jsonOut); err != nil {
		failErrCode(ExitInternal, err)
	}
}

func handleSetRelay(args []string) {
	fs := flag.NewFlagSet("set-relay", flag.ExitOnError)
	url := fs.String("url", "", "relay URL (e.g. http://relay.example.com:3032)")
	name := fs.String("name", "", "this node's name on the relay")
	secret := fs.String("secret", "", "relay shared secret (Bearer token, or set RELAY_SECRET env var)")
	secretStdin := fs.Bool("secret-stdin", false, "read the relay secret from stdin (keeps it out of process lists and daemon logs)")
	fs.Parse(args)

	if *secretStdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			failErrCode(ExitInternal, fmt.Errorf("reading secret from stdin: %w", err))
		}
		*secret = strings.TrimSpace(string(b))
		if *secret == "" {
			fail(ExitConfigError, "missing_argument", "--secret-stdin: no secret on stdin",
				"printf %s <secret> | remotecmd-cli set-relay --secret-stdin")
		}
	}

	// --secret alone updates only the secret, leaving the relay URL and this
	// node's name as configured (rolling a secret out over rcx must not
	// rewrite every node's relay settings).
	secretOnly := *url == "" && *name == "" && *secret != ""
	if !secretOnly && (*url == "" || *name == "") {
		fail(ExitConfigError, "missing_argument", "--url and --name are required (or only --secret / --secret-stdin to change just the secret)",
			"remotecmd-cli set-relay --url <u> --name <n> [--secret <s>]",
			"printf %s <secret> | remotecmd-cli set-relay --secret-stdin")
	}

	if !secretOnly {
		if err := setRelay(*url, *name); err != nil {
			failErrCode(ExitInternal, err)
		}
	}
	if *secret != "" {
		if err := setRelaySecret(*secret); err != nil {
			failErrCode(ExitInternal, fmt.Errorf("saving secret: %w", err))
		}
	}
	if !secretOnly {
		fmt.Printf("Relay configured: %s (as %q)\n", *url, *name)
	}
	if *secret != "" {
		fmt.Println("Relay secret saved to config")
	}
}
