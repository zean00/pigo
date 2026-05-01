# MCP

`pigo` includes an MCP adapter so a headless ACP session can expose external MCP tools to the model.

MCP tools are loaded per session, converted into model-facing tool definitions, invoked when the model requests them, and reported back through the normal runtime and ACP event streams.

## Supported Transports

- `stdio`
- `http`
- `sse`

## Configuration Sources

MCP servers are loaded in this order:

1. ACP `mcpServers` parameters.
2. `PI_MCP_CONFIG_JSON`.
3. `PI_MCP_CONFIG`.
4. `.pi/mcp.json`.
5. `~/.pi/agent/mcp.json`.

ACP-provided servers take precedence because they are session-specific and supplied by the host client.

## Example Config

```json
{
  "servers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "remote": {
      "type": "http",
      "url": "http://localhost:3000/mcp",
      "headers": {
        "Authorization": "Bearer token"
      }
    }
  }
}
```

## Tool Naming

MCP tools are exposed to the model as:

```text
mcp__<server>__<tool>
```

For example, a `read_file` tool from a `filesystem` server becomes:

```text
mcp__filesystem__read_file
```

This avoids collisions with built-in coding-agent tools and keeps the origin server visible in the tool name.

## Tool Metadata

The MCP adapter includes MCP metadata in the model-facing tool spec where available. This allows clients and runtime diagnostics to preserve useful source information while still presenting a normalized tool schema to the provider layer.

## Progress Notifications

MCP progress notifications are forwarded as ACP tool updates. This lets ACP clients show long-running tool progress without waiting for the final MCP tool result.

## Failure Behavior

If an MCP server cannot be loaded or a tool invocation fails, the error is reported through the normal tool-result path. The agent loop can then continue with the model-visible tool error, which matches the behavior of built-in coding-agent tools.
