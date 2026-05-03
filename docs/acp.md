# ACP

ACP is the main integration boundary for `pigo`. The purpose of the port is to provide a headless coding agent that can be used by another project and communicate through ACP.

`cmd/pigo-acp` starts a JSON-RPC server over stdio:

```bash
go run ./cmd/pigo-acp
```

## Initialize

Example request:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}
```

The server advertises the `pigo-acp` agent, filesystem capabilities, MCP transport capabilities, and session lifecycle capabilities.

## Supported Session Methods

- `session/new`
- `session/list`
- `session/load`
- `session/resume`
- `session/fork`
- `session/prompt`
- `session/cancel`
- `session/close`
- `session/set_model`
- `session/set_mode`

Authentication methods are also exposed:

- `authenticate`
- `logout`

## Session Creation

Example `session/new` request:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/new",
  "params": {
    "cwd": "/path/to/workspace",
    "mcpServers": []
  }
}
```

ACP sessions are backed by `pkg/codingagent.Session`. A session owns the workspace root, selected model/mode, runtime event stream, optional session store, OAuth credentials, and MCP registry.

## Prompting

Example `session/prompt` request:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "session/prompt",
  "params": {
    "sessionId": "session-id",
    "prompt": [
      {"type": "text", "text": "Inspect the project and summarize the test command."}
    ]
  }
}
```

During a prompt, the server emits `session/update` notifications for assistant chunks, tool execution, tool output, and completion.

## Loading and Forking

`session/load` and `session/resume` restore persisted sessions from a JSONL session file. Loaded history is replayed to ACP clients so the client can reconstruct the transcript.

`session/fork` creates a new branch from an existing persisted session state.

## Cancellation

`session/cancel` cancels an active prompt:

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "session/cancel",
  "params": {
    "sessionId": "session-id"
  }
}
```

The active provider/tool context is canceled where supported, and the runtime emits cancellation-aware completion or error events.

## MCP Through ACP

ACP clients can pass `mcpServers` when creating, loading, resuming, or forking a session. Those servers are added to the session MCP registry and exposed as model tools.

## Command Compression Through ACP

ACP clients can configure command-output compression with `session/set_config_option`:

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "session/set_config_option",
  "params": {
    "sessionId": "session-id",
    "configId": "command_compression",
    "value": "auto"
  }
}
```

The related options are:

- `command_compression`: `off`, `auto`, or `force`.
- `command_compression_enabled_filters`: comma-separated filter allowlist.
- `command_compression_disabled_filters`: comma-separated filter denylist.

Bash tool updates include compression metadata in the tool result details.

## Bash Permissions Through ACP

ACP clients can configure bash command permissions with `session/set_config_option`:

```json
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "session/set_config_option",
  "params": {
    "sessionId": "session-id",
    "configId": "bash_permission_mode",
    "value": "allow-list"
  }
}
```

The related options are:

- `bash_permission_mode`: `allow-all` or `allow-list`.
- `bash_permission_allow`: comma-separated `exact:`, `glob:`, or `regex:` rules.
- `bash_permission_deny`: comma-separated `exact:`, `glob:`, or `regex:` rules.

Deny rules take precedence. Denied commands return an error-style tool result and are not executed.

## Built-in Tools Through ACP

ACP clients can configure which built-in model tools are exposed with `session/set_config_option`:

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "method": "session/set_config_option",
  "params": {
    "sessionId": "session-id",
    "configId": "builtin_tools_disabled",
    "value": "bash,write"
  }
}
```

The related options are:

- `builtin_tools_enabled`: comma-separated list of built-in tools to expose.
- `builtin_tools_disabled`: comma-separated list of built-in tools to remove.

Available built-in tools are `bash`, `write`, `read`, `edit`, `ls`, `grep`, and `find`. Disabled tools take precedence over enabled tools. MCP tools and extension tools are configured separately.
