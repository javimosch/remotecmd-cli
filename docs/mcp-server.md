# rcmd MCP Server

rcmd includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server that lets AI agents manage your servers directly — no SSH, no shell parsing.

## Quick start

```bash
# 1. Make sure rcmd is configured with at least one target
rcmd add-target --name prod --token <your-token>
rcmd list-targets

# 2. Test the MCP server
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | rcmd mcp
```

## Configure with AI agents

### Claude Code

Add to `~/.claude/mcp.json`:

```json
{
  "mcpServers": {
    "rcmd": {
      "command": "rcmd",
      "args": ["mcp"]
    }
  }
}
```

### Cursor

Add to `.cursor/mcp.json` in your project:

```json
{
  "mcpServers": {
    "rcmd": {
      "command": "rcmd",
      "args": ["mcp"]
    }
  }
}
```

### Windsurf

Add to `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "rcmd": {
      "command": "rcmd",
      "args": ["mcp"]
    }
  }
}
```

### Devin CLI

Add to `.devin/config.json`:

```json
{
  "mcpServers": {
    "rcmd": {
      "command": "rcmd",
      "args": ["mcp"]
    }
  }
}
```

## Available tools

| Tool | Description |
|------|-------------|
| `rcmd_list_targets` | List all configured remote targets |
| `rcmd_exec` | Execute a shell command on a remote target |
| `rcmd_cp` | Copy a local file to a remote target |
| `rcmd_health` | Check if a target is reachable and get latency |

## Example: What AI agents can do

Once configured, an AI agent can:

- **Check server health:** "Is the prod server reachable?"
- **Run commands:** "Check disk space on prod" → agent calls `rcmd_exec` with `df -h`
- **Deploy files:** "Copy the new config to staging" → agent calls `rcmd_cp`
- **Troubleshoot:** "Check nginx logs on prod" → agent calls `rcmd_exec` with `tail -50 /var/log/nginx/error.log`
- **Multi-step ops:** "Restart nginx on all servers and verify" → agent calls `rcmd_exec` multiple times

## How it works

```
AI Agent (Claude/Cursor/etc.)
    ↓ MCP (JSON-RPC over stdio)
rcmd mcp (local MCP server)
    ↓ WebSocket
rcmd relay
    ↓ WebSocket
rcmd daemon (on your server)
    ↓
shell command
```

The MCP server runs locally as a subprocess of the AI agent. It reads your existing rcmd config (`~/.remotecmd/config.json`) for targets and tokens. All communication goes through the rcmd relay — no SSH, no open ports, no keys.

## Security

- The MCP server only has access to targets you've already configured with `rcmd add-target`
- All traffic goes through the rcmd relay with token-based auth
- No credentials are exposed to the AI agent — it only sees target names
- Self-host the relay for full control (see [self-hosting guide](self-hosting.md))
