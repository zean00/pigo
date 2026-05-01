# pigo

`pigo` is an attempt to port selected parts of the original [`pi-mono`](https://github.com/zean00/pi-mono) project to Go.

The purpose of this port is to create a headless coding agent that can be used by another project and communicate through ACP. The project focuses on the runtime pieces needed for provider calls, agent loops, workspace tools, session handling, ACP integration, and MCP tool attachment rather than rebuilding the original interactive UI surfaces.

## Documentation

The full documentation is in `docs/` and can be built with MkDocs:

```bash
python -m pip install mkdocs
mkdocs build
mkdocs serve
```

Start here:

- [Overview](docs/index.md)
- [Installation](docs/installation.md)
- [Configuration](docs/configuration.md)
- [Architecture](docs/architecture.md)
- [How It Works](docs/how-it-works.md)
- [Comparison with original pi-mono](docs/pi-mono-comparison.md)
- [ACP](docs/acp.md)
- [MCP](docs/mcp.md)

## Scope

- `pkg/ai`: provider-independent message, stream, tool-call, model, auth, usage, and normalization contracts.
- `pkg/agentcore`: model/tool loop, event streaming, session state, and provider request behavior.
- `pkg/codingagent`: headless coding-agent runtime for workspace operations, session storage, hooks, OAuth, and model selection.
- `pkg/acpadapter`: ACP stdio adapter for host applications.
- `pkg/mcpadapter`: MCP server registry and tool bridge.

Out of scope:

- TUI implementation.
- Web UI or product-specific UI surfaces.
- Full TypeScript SDK ergonomics.

## Quick Start

Run tests:

```bash
go test ./...
```

Run the ACP stdio adapter:

```bash
go run ./cmd/pigo-acp
```

Run the minimal JSONL RPC server:

```bash
printf '{"id":"s1","type":"get_state"}\n' | go run ./cmd/pigo-rpc
```

Run the interactive OAuth helper:

```bash
go run ./cmd/pigo-auth --list
go run ./cmd/pigo-auth --provider anthropic --auth-file tmp/auth.json
```

## ACP and MCP

`pigo-acp` implements the ACP session flow for creating, listing, loading, resuming, forking, prompting, canceling, and closing headless coding-agent sessions.

MCP servers can be attached to ACP sessions. MCP config is read from ACP `mcpServers` first, then from `PI_MCP_CONFIG_JSON`, `PI_MCP_CONFIG`, `.pi/mcp.json`, and `~/.pi/agent/mcp.json`. Supported MCP transports are `stdio`, `http`, and `sse`; tools are exposed as `mcp__<server>__<tool>`.

## Command Output Compression

`pigo` includes an RTK-inspired command-output compression layer for bash command results. Compression is command-aware, configurable through environment variables plus ACP/RPC session settings, and preserves command execution semantics. See [Configuration](docs/configuration.md#command-output-compression).

## Providers

The provider layer includes OpenAI-compatible, Anthropic-compatible, Mistral, Google, Google Vertex, Google Gemini CLI-style, OpenAI Codex, and Amazon Bedrock transports.

Local LLM servers such as Ollama, llama.cpp server, and LM Studio can be used through the OpenAI-compatible transport when they expose a `/v1/chat/completions` API. See [Configuration](docs/configuration.md#local-llms).

## Validation

Run fixture conformance:

```bash
make verify-conformance
```

Run behavior parity against a local `pi-mono` checkout at `../pi-mono`:

```bash
make parity
```

Run the optional live OpenRouter smoke check:

```bash
OPENROUTER_API_KEY=... make live-openrouter-smoke
```

Refresh the generated model catalog from the TypeScript source:

```bash
make generate-models
```

The tracked Go target is intended to match the comparable `pi-mono` headless runtime behavior. Interactive TUI/product surfaces remain outside the Go target unless scope changes.
