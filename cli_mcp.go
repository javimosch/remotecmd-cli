package main

import (
	"fmt"
	"os"
)

// handleMCP starts the MCP server in stdio mode.
// Usage: rcmd mcp
func handleMCP(args []string) {
	// MCP server communicates over stdin/stdout — suppress all logs to stdout
	suppressLog()

	// Check that we have a config with at least one target
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rcmd mcp: no config found. Run 'rcmd add-target' first.\n")
		os.Exit(ExitConfigError)
	}
	if len(cfg.Targets) == 0 {
		fmt.Fprintf(os.Stderr, "rcmd mcp: no targets configured. Run 'rcmd add-target --name <n> --token <t>' first.\n")
		os.Exit(ExitConfigError)
	}

	runMCPServer()
}

func printMCPHelp() {
	fmt.Println(`MCP SERVER (Model Context Protocol):

  rcmd mcp                Start the MCP server (stdio mode)

The MCP server exposes rcmd as a tool for AI agents (Claude, Cursor,
Windsurf, etc.). Agents can execute commands, copy files, check health,
and manage cron jobs on your servers — without SSH.

Tools exposed:
  rcmd_list_targets       List configured targets
  rcmd_exec               Execute a command on a target
  rcmd_cp                 Copy a file to a target
  rcmd_cron_list          List scheduled jobs
  rcmd_cron_logs          View cron job logs
  rcmd_health             Check target health

Configuration examples:

  Claude Code (~/.claude/mcp.json):
    {
      "mcpServers": {
        "rcmd": {
          "command": "rcmd",
          "args": ["mcp"]
        }
      }
    }

  Cursor (.cursor/mcp.json):
    {
      "mcpServers": {
        "rcmd": {
          "command": "rcmd",
          "args": ["mcp"]
        }
      }
    }

  Windsurf (~/.codeium/windsurf/mcp_config.json):
    {
      "mcpServers": {
        "rcmd": {
          "command": "rcmd",
          "args": ["mcp"]
        }
      }
    }`)
}
