# Configuration

`pigo` can be configured through command flags, provider environment variables, OAuth stores, model/provider registration, and MCP server configuration.

## Command Flags

### `pigo-acp`

```bash
go run ./cmd/pigo-acp --auth-file tmp/auth.json
```

- `--auth-file`: optional OAuth credential store used by ACP sessions.

### `pigo-rpc`

```bash
go run ./cmd/pigo-rpc --cwd . --session-file tmp/session.jsonl --auth-file tmp/auth.json
```

- `--cwd`: workspace root.
- `--session-file`: optional JSONL session file.
- `--auth-file`: optional OAuth credential store.

## Provider Environment Variables

Provider API keys are read from environment variables. Common examples:

| Provider | Environment variable |
| --- | --- |
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` or `ANTHROPIC_OAUTH_TOKEN` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Google Gemini | `GEMINI_API_KEY` |
| Google Vertex | `GOOGLE_CLOUD_API_KEY` |
| Mistral | `MISTRAL_API_KEY` |
| Groq | `GROQ_API_KEY` |
| Cerebras | `CEREBRAS_API_KEY` |
| xAI | `XAI_API_KEY` |
| Hugging Face router | `HF_TOKEN` |
| Amazon Bedrock | AWS standard credentials or `AWS_BEARER_TOKEN_BEDROCK` |

## OAuth

Use the OAuth helper to list supported OAuth providers:

```bash
go run ./cmd/pigo-auth --list
```

Start an OAuth flow:

```bash
go run ./cmd/pigo-auth --provider anthropic --auth-file tmp/auth.json
go run ./cmd/pigo-auth --provider openai-codex --auth-file tmp/auth.json
```

Google Gemini CLI and Antigravity OAuth require caller-provided desktop OAuth client credentials:

```bash
export PIGO_GEMINI_CLI_OAUTH_CLIENT_ID=...
export PIGO_GEMINI_CLI_OAUTH_CLIENT_SECRET=...
export PIGO_ANTIGRAVITY_OAUTH_CLIENT_ID=...
export PIGO_ANTIGRAVITY_OAUTH_CLIENT_SECRET=...
```

OAuth secrets are stored through the dedicated OAuth store, not normal session history.

## Local LLMs

Local LLM servers are supported through the OpenAI-compatible transport when they expose a `/v1/chat/completions` API. This covers common local runtimes such as Ollama, llama.cpp server, and LM Studio.

Typical base URLs:

| Runtime | Base URL |
| --- | --- |
| Ollama | `http://localhost:11434/v1` |
| llama.cpp server | `http://localhost:8080/v1` |
| LM Studio | `http://localhost:1234/v1` |

Register a custom provider/model with `API: "openai-completions"` and point `BaseURL` at the local server:

```go
err := ai.RegisterProviderConfig(ai.ProviderConfig{
	Name:    "ollama-local",
	API:     "openai-completions",
	BaseURL: "http://localhost:11434/v1",
	APIKey:  "local",
	Models: []ai.Model{{
		ID: "llama3.1",
	}},
})
```

Most local servers ignore the API key, but `RegisterProviderConfig` currently requires a non-empty value when defining models, so use a harmless placeholder such as `local`.

## Command Output Compression

`pigo` can compress noisy bash command output before it is stored in session history or sent back to the model. This is inspired by RTK-style command filtering, but implemented natively inside the headless coding-agent runtime.

Compression never changes the executed command, exit code, or cancellation behavior. It only changes the output text recorded for the agent and client, and it includes metadata such as the filter used, original byte count, compressed byte count, and truncation state.

Environment defaults:

```bash
export PIGO_COMMAND_COMPRESSION=auto
export PIGO_COMMAND_COMPRESSION_ENABLE=go-test,git-diff
export PIGO_COMMAND_COMPRESSION_DISABLE=generic
export PIGO_COMMAND_COMPRESSION_MAX_BYTES=20000
```

Modes:

| Mode | Behavior |
| --- | --- |
| `off` | Disable command-aware compression and keep only the existing hard output limit. |
| `auto` | Compress when output is large or when a command-specific filter is useful. |
| `force` | Run the best matching enabled filter even for smaller output. |

Built-in filters:

| Filter | Commands |
| --- | --- |
| `go-test` | `go test ...` |
| `git-diff` | `git diff ...` |
| `git-status` | `git status ...` |
| `grep` | `rg ...`, `grep ...` |
| `list` | `ls ...`, `find ...` |
| `generic` | fallback long-output truncation |

ACP sessions expose these config options:

- `command_compression`
- `command_compression_enabled_filters`
- `command_compression_disabled_filters`

The JSONL RPC adapter supports:

- `set_command_compression`
- `get_command_compression`

## MCP Configuration

MCP config is loaded in this order:

1. ACP `mcpServers` session parameters.
2. `PI_MCP_CONFIG_JSON`.
3. `PI_MCP_CONFIG`.
4. `.pi/mcp.json`.
5. `~/.pi/agent/mcp.json`.

Example JSON file:

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
      "url": "http://localhost:3000/mcp"
    }
  }
}
```

Set a config path:

```bash
export PI_MCP_CONFIG=/path/to/mcp.json
```

Or inline JSON:

```bash
export PI_MCP_CONFIG_JSON='{"servers":{"demo":{"type":"http","url":"http://localhost:3000/mcp"}}}'
```
