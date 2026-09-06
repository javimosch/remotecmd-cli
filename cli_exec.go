package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// stdinFirstByteWait is how long to wait for the first byte of piped stdin
// before deciding there is none. Once a byte has arrived the rest is read
// without a deadline, so a slow producer is only at risk before it writes
// anything at all.
var stdinFirstByteWait = 300 * time.Millisecond

// StdinWaitEnv overrides that wait, for a producer whose first byte is slow.
const StdinWaitEnv = "REMOTECMD_STDIN_WAIT"

// readPipedStdin returns data piped into the process, or nil if there is none.
//
// It must never block waiting for stdin that is not coming. "Not a terminal"
// is not the same as "somebody is about to send me data": an inherited pipe
// with no writer is the normal shape of stdin under an agent harness, CI,
// nohup, systemd and cron. Reading it to EOF there waits forever, and because
// this runs before the request is built, the command never reaches the daemon
// at all - the tool looks hung rather than failing.
//
// ssh has the same input to work with and never lets stdin delay the command:
// it forwards stdin alongside execution instead of gating on it. remotecmd
// sends stdin in the request, so it cannot stream - but it can refuse to wait
// for a first byte that never arrives.
func readPipedStdin() []byte {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil // terminal — no piped stdin
	}
	// A regular file or a redirect from one is bounded and already there;
	// reading it fully cannot hang.
	if stat.Mode().IsRegular() {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil
		}
		return data
	}
	return readPipeWithGrace(os.Stdin, stdinWait())
}

func stdinWait() time.Duration {
	if raw := os.Getenv(StdinWaitEnv); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			return d
		}
	}
	return stdinFirstByteWait
}

// readPipeWithGrace reads r fully, but gives up if nothing at all arrives
// within grace. A negative or zero grace means wait indefinitely, which is
// what an explicit --stdin asks for.
func readPipeWithGrace(r io.Reader, grace time.Duration) []byte {
	type result struct {
		data []byte
		err  error
	}
	first := make(chan result, 1)
	rest := make(chan result, 1)

	go func() {
		// One byte is the whole question: is anybody writing?
		head := make([]byte, 1)
		n, err := io.ReadFull(r, head)
		if n == 0 {
			first <- result{nil, err}
			return
		}
		first <- result{head[:n], nil}
		// Somebody is writing, so the rest is worth waiting for however long
		// it takes - a slow producer is only ambiguous before its first byte.
		tail, err := io.ReadAll(r)
		rest <- result{tail, err}
	}()

	if grace <= 0 {
		head := <-first
		if head.data == nil {
			return nil
		}
		tail := <-rest
		return append(head.data, tail.data...)
	}

	select {
	case head := <-first:
		if head.data == nil {
			return nil // EOF or error before any data: no stdin
		}
		tail := <-rest
		if tail.err != nil {
			return head.data
		}
		return append(head.data, tail.data...)
	case <-time.After(grace):
		// Nobody is writing. The goroutine stays parked on a read that will
		// end when the process does; abandoning it costs one goroutine and
		// saves an unbounded wait.
		return nil
	}
}

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

	stdinData := readPipedStdin()
	if err := handleExecWithStdin(*target, *cmd, *timeout, *stream, stdinData); err != nil {
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
		stdinData := readPipedStdin()
		if err := handleExecWithStdin(targetList[0], *cmd, *timeout, *stream, stdinData); err != nil {
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
