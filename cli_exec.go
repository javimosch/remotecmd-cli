package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func handleExecFlags(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	cmd := fs.String("cmd", "", "command to execute")
	timeout := fs.Int("timeout", 30, "command timeout in seconds")
	stream := fs.Bool("stream", false, "stream output in real time")
	fs.Parse(args)

	if *target == "" || *cmd == "" {
		fmt.Fprintln(os.Stderr, "Error: --target and --cmd are required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli --target <name> --cmd <command> [--timeout <seconds>] [--stream]")
		osExit(ExitConfigError)
	}

	if err := handleExec(*target, *cmd, *timeout, *stream); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(classifyError(err))
	}
}

func handleExecSubcommand(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	target := fs.String("target", "", "single target machine name")
	targets := fs.String("targets", "", "comma-separated target names")
	group := fs.String("group", "", "target group name")
	cmd := fs.String("cmd", "", "command to execute")
	timeout := fs.Int("timeout", 30, "command timeout in seconds")
	stream := fs.Bool("stream", false, "stream output (single target only)")
	parallel := fs.Int("parallel", 0, "max parallel targets (multi-target only)")
	format := fs.String("format", "table", "output format: json or table (multi-target only)")
	fs.Parse(args)

	if *cmd == "" {
		fmt.Fprintln(os.Stderr, "Error: --cmd is required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli exec --cmd <command> [--target <name> | --targets <list> | --group <name>] [--timeout <s>] [--stream] [--format json|table]")
		osExit(ExitConfigError)
	}

	var targetList []string
	isMulti := false

	if *group != "" {
		var err error
		targetList, err = resolveTargets(*group, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(ExitConfigError)
		}
		isMulti = len(targetList) > 1
	} else if *targets != "" {
		var err error
		targetList, err = resolveTargets(*targets, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(ExitConfigError)
		}
		isMulti = len(targetList) > 1
	} else if *target != "" {
		targetList = []string{*target}
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(classifyError(err))
		}
		if _, ok := cfg.Targets[*target]; !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown target %q\n", *target)
			osExit(ExitConfigError)
		}
		isMulti = false
	} else {
		fmt.Fprintln(os.Stderr, "Error: one of --target, --targets, or --group is required")
		osExit(ExitConfigError)
	}

	_ = parallel

	if isMulti {
		if *stream {
			fmt.Fprintln(os.Stderr, "Warning: --stream is not supported for multi-target; ignoring")
		}
		if err := handleMultiExec(targetList, *cmd, *timeout, *format); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(classifyError(err))
		}
	} else {
		if err := handleExec(targetList[0], *cmd, *timeout, *stream); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			osExit(classifyError(err))
		}
	}
}

func handleGroupSubcommand(args []string) {
	if len(args) < 1 {
		printGroupHelp()
		osExit(ExitConfigError)
	}
	switch args[0] {
	case "create":
		handleGroupCreate(args[1:])
	case "delete":
		handleGroupDelete(args[1:])
	case "add":
		handleGroupAdd(args[1:])
	case "remove":
		handleGroupRemove(args[1:])
	case "list":
		handleGroupList()
	default:
		printGroupHelp()
		osExit(ExitConfigError)
	}
}

func handleGroupCreate(args []string) {
	fs := flag.NewFlagSet("group create", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	targets := fs.String("targets", "", "comma-separated target names")
	fs.Parse(args)

	if *name == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --targets are required")
		osExit(ExitConfigError)
	}

	list := strings.Split(*targets, ",")
	for i, t := range list {
		list[i] = strings.TrimSpace(t)
	}

	if err := groupCreate(*name, list); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	fmt.Printf("Group %q created with %d targets\n", *name, len(list))
}

func handleGroupDelete(args []string) {
	fs := flag.NewFlagSet("group delete", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		osExit(ExitConfigError)
	}

	if err := groupDelete(*name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	fmt.Printf("Group %q deleted\n", *name)
}

func handleGroupAdd(args []string) {
	fs := flag.NewFlagSet("group add", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	targets := fs.String("targets", "", "comma-separated target names")
	fs.Parse(args)

	if *name == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --targets are required")
		osExit(ExitConfigError)
	}

	list := strings.Split(*targets, ",")
	for i, t := range list {
		list[i] = strings.TrimSpace(t)
	}

	if err := groupAddTargets(*name, list); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	fmt.Printf("Targets added to group %q\n", *name)
}

func handleGroupRemove(args []string) {
	fs := flag.NewFlagSet("group remove", flag.ExitOnError)
	name := fs.String("name", "", "group name")
	targets := fs.String("targets", "", "comma-separated target names")
	fs.Parse(args)

	if *name == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --targets are required")
		osExit(ExitConfigError)
	}

	list := strings.Split(*targets, ",")
	for i, t := range list {
		list[i] = strings.TrimSpace(t)
	}

	if err := groupRemoveTargets(*name, list); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
	}
	fmt.Printf("Targets removed from group %q\n", *name)
}

func handleGroupList() {
	if err := groupList(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitInternal)
	}
}
