package main

import (
	"encoding/json"
	"fmt"
)

// help-json: the machine-readable command catalog (cli-output-spec §4).
// `guide` is the mental model; this is the list of what exists. Keep it in
// step with main()'s dispatcher — TestHelpJSONCoversDispatcher fails when a
// top-level command is added without a catalog entry.

type catalogCommand struct {
	Args  []string `json:"args"`
	Flags []string `json:"flags,omitempty"`
	// Auth: needs a configured relay and, for target commands, the
	// target's token in config.json.
	Auth    bool   `json:"auth"`
	Summary string `json:"summary"`
}

func helpCatalog() map[string]any {
	c := map[string]catalogCommand{
		"exec": {Args: nil, Auth: true,
			Flags:   []string{"--target <name>", "--targets <t1,t2,...>", "--group <name>", "--cmd <command>", "--timeout <s>", "--stream", "--parallel <n>", "--format json|table"},
			Summary: "run a command on one target, a list, or a group"},
		"--target": {Args: nil, Auth: true,
			Flags:   []string{"--target <name>", "--cmd <command>", "--timeout <s>", "--stream"},
			Summary: "legacy single-target form: remotecmd-cli --target <name> --cmd <command>"},
		"cp": {Args: nil, Auth: true,
			Flags:   []string{"--target <name>", "--src <path>", "--dst <path>", "--stream"},
			Summary: "copy a local file or directory to a target"},
		"tunnel start": {Args: nil, Auth: true,
			Flags:   []string{"--target <name>", "--local <port>", "--remote <host:port>", "-daemon"},
			Summary: "forward a local port through a target (like ssh -L); binds 127.0.0.1"},
		"tunnel stop": {Args: nil, Auth: false,
			Flags: []string{"--target <name>", "--local <port>"}, Summary: "stop a background tunnel"},
		"tunnel status": {Args: nil, Auth: false, Summary: "list background tunnels"},
		"add-target": {Args: nil, Auth: false,
			Flags: []string{"--name <n>", "--token <t>"}, Summary: "save a target's name and token"},
		"remove-target": {Args: nil, Auth: false,
			Flags: []string{"--name <n>"}, Summary: "forget a target"},
		"list-targets": {Args: nil, Auth: true,
			Flags:   []string{"--refresh", "--json", "--no-health"},
			Summary: "list targets with health (probes nodes unseen for >1h)"},
		"set-relay": {Args: nil, Auth: false,
			Flags:   []string{"--url <u>", "--name <n>", "--secret <s>"},
			Summary: "configure the relay URL, this node's name, and the relay secret"},
		"group create": {Flags: []string{"--name <n>", "--targets <t1,t2,...>"}, Summary: "create a target group"},
		"group add":    {Flags: []string{"--name <n>", "--targets <t1,t2,...>"}, Summary: "add targets to a group"},
		"group remove": {Flags: []string{"--name <n>", "--targets <t1,t2,...>"}, Summary: "remove targets from a group"},
		"group delete": {Flags: []string{"--name <n>"}, Summary: "delete a group"},
		"group list":   {Summary: "list groups"},
		"daemon start": {Auth: false,
			Flags:   []string{"--token <t>", "--name <n>", "-daemon"},
			Summary: "run the target daemon on this machine (dials out to the relay); --name runs an extra instance under another name"},
		"daemon stop": {Flags: []string{"--name <n>"},
			Summary: "stop the background target daemon (no-op if not running)"},
		"daemon update": {Flags: []string{"--name <n>", "--check", "--force"},
			Summary: "update the binary the running daemon(s) use from the latest release (sha256-verified) and restart them in place"},
		"daemon status": {Flags: []string{"--name <n>", "--json"},
			Summary: "show the background target daemon's state; --json exits 3 when stopped"},
		"daemon " + persistenceSubcommandName(): {Args: []string{"install|remove"},
			Summary: "install or remove the daemon as a boot service"},
		"relay daemon start": {Flags: []string{"--port <n>", "--host <addr>", "-daemon", "--tls-cert <file>", "--tls-key <file>"},
			Summary: "run the relay hub (all interfaces unless --host); GET /_health and /health"},
		"relay daemon stop": {Summary: "stop the background relay (no-op if not running)"},
		"relay daemon update": {Flags: []string{"--check", "--force"},
			Summary: "update the running relay's binary from the latest release and restart it in place"},
		"relay daemon status": {Flags: []string{"--json"},
			Summary: "show the background relay's state; --json exits 3 when stopped"},
		"relay add-key":    {Args: []string{"key"}, Summary: "add an activation key (hot-reloaded)"},
		"relay remove-key": {Args: []string{"key"}, Summary: "remove an activation key"},
		"relay list-keys":  {Summary: "list activation keys (masked)"},
		"pair listen": {Auth: true,
			Flags:   []string{"--name <n>", "--timeout <s>", "--code <c>", "--require-activation-key"},
			Summary: "wait for a new node to join; prints its install one-liner"},
		"pair accept": {Flags: []string{"--code <c>", "--activation-key <k>"},
			Summary: "join from the new node using a pair code"},
		"pair disconnect": {Auth: true, Flags: []string{"--target <name>"},
			Summary: "tell a paired daemon to exit (kill switch)"},
		"sidecar activate": {Flags: []string{"--url <u>", "--relay <r>", "--code <c>", "--activation-key <k>", "--name <n>", "--path <p>", "--timeout <s>"},
			Summary: "activate a sidecar inside a remote container"},
		"alias install":   {Summary: "install rc, rcx, rcl, rcs, rcc, rcg, rcd, rcr"},
		"alias uninstall": {Summary: "remove the installed aliases"},
		"client": {Auth: true,
			Summary: "persistent session: one JSON command per line on stdin"},
		"mcp":       {Summary: "MCP server over stdio for AI agents"},
		"feedback":  {Args: []string{"message"}, Flags: []string{"--kind bug|idea|praise|note", "--context <c>"}, Summary: "send feedback to the maintainers (never fails)"},
		"update":    {Flags: []string{"--check", "--force"}, Summary: "self-update from GitHub releases (sha256-verified)"},
		"version":   {Flags: []string{"--json"}, Summary: "print the version"},
		"guide":     {Flags: []string{"--human"}, Summary: "the mental model: loop, concepts, gotchas (JSON; --human for markdown)"},
		"help":      {Summary: "human-readable help"},
		"help-json": {Summary: "this catalog"},
	}
	// Normalise nil slices so every entry carries "args": [].
	for k, v := range c {
		if v.Args == nil {
			v.Args = []string{}
			c[k] = v
		}
	}
	return map[string]any{
		"tool":     "remotecmd-cli",
		"version":  Version,
		"output":   "text; JSON with --format json / --json; typed errors on stderr",
		"commands": c,
		"exit_codes": map[string]string{
			"0":   "success",
			"1":   "remote command failed or a target returned an error",
			"2":   "cannot reach the relay (recoverable: retry with backoff)",
			"3":   "invalid arguments or missing config",
			"4":   "internal error",
			"5":   "update --check: a newer release is available (not an error)",
			"100": "update failed (download, verification, smoke test or swap)",
		},
		"env": []string{
			"RCMD_CONFIG_DIR", "RCMD_ERROR_FORMAT", "RCMD_NO_NUDGE",
			"RCMD_CHUNK_SIZE", "RCMD_PARALLEL_STREAMS",
			"RCMD_TLS_CERT", "RCMD_TLS_KEY", "RCMD_TLS_SKIP_VERIFY",
			"RELAY_SECRET", "RELAY_SECRET_EXEMPT", "REMOTECMD_FEEDBACK_RELAY",
		},
		"see_also": []string{"remotecmd-cli guide", "remotecmd-cli help"},
	}
}

func handleHelpJSON() {
	b, _ := json.MarshalIndent(helpCatalog(), "", "  ")
	fmt.Println(string(b))
}
