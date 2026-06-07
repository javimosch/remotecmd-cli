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
		os.Exit(ExitConfigError)
	}

	if err := addTarget(*name, *token); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitInternal)
	}
	fmt.Printf("Target %q added\n", *name)
}

func handleRemoveTarget(args []string) {
	fs := flag.NewFlagSet("remove-target", flag.ExitOnError)
	name := fs.String("name", "", "target name")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		os.Exit(ExitConfigError)
	}

	if err := removeTarget(*name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitInternal)
	}
	fmt.Printf("Target %q removed\n", *name)
}

func handleListTargets() {
	if err := listTargets(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitInternal)
	}
}

func handleSetRelay(args []string) {
	fs := flag.NewFlagSet("set-relay", flag.ExitOnError)
	url := fs.String("url", "", "relay URL (e.g. http://dk1:3032)")
	name := fs.String("name", "", "this node's name on the relay")
	fs.Parse(args)

	if *url == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --url and --name are required")
		os.Exit(ExitConfigError)
	}

	if err := setRelay(*url, *name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitInternal)
	}
	fmt.Printf("Relay configured: %s (as %q)\n", *url, *name)
}
