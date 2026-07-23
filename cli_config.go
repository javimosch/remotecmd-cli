package main

import (
	"flag"
	"fmt"
	"os"
)

func handleAddTarget(args []string) {
	fs := flag.NewFlagSet("add-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	token := fs.String("token", "", "auth token")
	fs.Parse(args)

	if *name == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --token are required")
		osExit(ExitConfigError)
	}

	if err := addTarget(*name, *token); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitInternal)
	}
	fmt.Printf("Target %q added\n", *name)
}

func handleRemoveTarget(args []string) {
	fs := flag.NewFlagSet("remove-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		osExit(ExitConfigError)
	}

	if err := removeTarget(*name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitInternal)
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitInternal)
	}
}

func handleSetRelay(args []string) {
	fs := flag.NewFlagSet("set-relay", flag.ExitOnError)
	url := fs.String("url", "", "relay URL (e.g. http://relay.example.com:3032)")
	name := fs.String("name", "", "this node's name on the relay")
	fs.Parse(args)

	if *url == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --url and --name are required")
		osExit(ExitConfigError)
	}

	if err := setRelay(*url, *name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitInternal)
	}
	fmt.Printf("Relay configured: %s (as %q)\n", *url, *name)
}
