# pigo

`pigo` is an attempt to port selected parts of the original [`pi-mono`](https://github.com/zean00/pi-mono) project to Go.

The purpose of this port is to create a headless coding agent that can be used by another project and communicate through ACP. The project focuses on the runtime pieces needed for provider calls, agent loops, workspace tools, session handling, ACP integration, MCP tool attachment, and optional A2A agent-to-agent interoperability rather than rebuilding the original interactive UI surfaces.

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
- [A2A](docs/a2a.md)
- [Orchestration](docs/orchestration.md)
- [MCP](docs/mcp.md)

## Scope

- `pkg/ai`: provider-independent message, stream, tool-call, model, auth, usage, and normalization contracts.
- `pkg/agentcore`: model/tool loop, event streaming, session state, and provider request behavior.
- `pkg/codingagent`: headless coding-agent runtime for workspace operations, session storage, hooks, internal modules, OAuth, and model selection.
- `pkg/acpadapter`: ACP stdio adapter for host applications.
- `pkg/a2a` and `pkg/a2aadapter`: A2A client/tool support and HTTP server adapter.
- `pkg/orchestrator`: optional A2A-backed supervisor and task-graph orchestration.
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

Run the A2A HTTP adapter:

```bash
go run ./cmd/pigo-a2a --addr 127.0.0.1:4388
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

## A2A

`pigo-a2a` exposes pigo as a remote Agent-to-Agent Protocol server over HTTP. It publishes an Agent Card at `/.well-known/agent-card.json` and supports JSON-RPC `message/send`, `message/stream`, `tasks/get`, and `tasks/cancel` on `/a2a`.

Remote A2A agents can also be exposed to the model as tools with `PIGO_A2A_TOOLS=on` and `PIGO_A2A_CONFIG_JSON`, `PIGO_A2A_CONFIG`, `.pi/a2a.json`, or `~/.pi/agent/a2a.json`. See [A2A](docs/a2a.md).

## Orchestration

`pigo` includes an optional A2A-backed supervisor/orchestrator module. When `PIGO_ORCHESTRATOR=on`, sessions can expose `delegate_task`, `orchestrate_task`, `orchestration_status`, and `cancel_orchestration` tools. The module uses configured A2A agents and persists run snapshots as session custom entries. It does not change the core agent loop or provider layer. See [Orchestration](docs/orchestration.md).

## Command Output Compression

`pigo` includes an RTK-inspired command-output compression layer for bash command results. Compression is command-aware, configurable through environment variables plus ACP/RPC session settings, and preserves command execution semantics. See [Configuration](docs/configuration.md#command-output-compression).

Optional live validation:

```bash
OPENROUTER_API_KEY=... go test ./pkg/codingagent -run TestLiveOpenRouterCommandCompression -count=1
```

## Bash Permissions

`pigo` can restrict bash command execution with allow and deny lists. Rules support `exact:`, `glob:`, and `regex:` matching, and deny rules take precedence. See [Configuration](docs/configuration.md#bash-command-permissions).

## Built-in Tools

The built-in coding tools are `bash`, `write`, `read`, `edit`, `ls`, `grep`, and `find`. They can be filtered with enabled/disabled tool lists so embedders can expose only the tool surface they want. See [Configuration](docs/configuration.md#built-in-tool-policy).

Tool execution can run in `parallel`, `sequential`, or `interleaved` mode. Interleaved mode returns one tool result to the model at a time so reasoning can continue between tool calls. See [Configuration](docs/configuration.md#tool-execution).

Sessions record provider-reported token usage and can optionally enforce per-session token/cost quotas. See [Configuration](docs/configuration.md#usage-ledger-and-quotas).

## Session Purpose

`pigo` defaults to a coding-agent prompt, but embedders can set a session purpose of `coding`, `generic`, `research`, or `readonly`. Purpose changes the system-prompt framing and context sections only; tool access is still controlled separately through built-in tool policy, bash permissions, research tools, MCP tools, and ACP/RPC config. See [Configuration](docs/configuration.md#session-purpose-and-context).

## Prompt Injection Guard

`pigo` includes an optional prompt-injection guard. It is disabled by default. When enabled, it marks configured tool-result sources as untrusted data, adds system-prompt guidance, and can block sensitive tools after untrusted content enters the session context. This is defense in depth; use it alongside built-in tool policy and bash permissions. See [Configuration](docs/configuration.md#prompt-injection-guard).

## Internal Modules

Session capabilities are wired through an internal module registry so features can register tools, ACP/RPC config options, RPC handlers, and session metadata behavior without hardcoding every new feature into the core runtime. Registration is atomic: failed modules are rolled back before returning an error. See [Architecture](docs/architecture.md#pkgcodingagent).

## Agent Profiles

`pigo` can load lightweight agent profiles, teams, and chains from `.pi/agents` and `~/.pi/agent/agents`. Profiles act as optional system-prompt/config overlays; teams and chains are exposed as metadata for host applications. pigo does not execute multi-agent workflows by itself. See [Configuration](docs/configuration.md#agent-profiles-and-workflow-metadata).

## Research Tools

Optional internet research tools can be exposed with `PIGO_RESEARCH_TOOLS=research,search,scrape,security_search`. They are disabled by default; `search` uses an external SearXNG instance from `PIGO_SEARXNG_URL` or `SEARXNG_URL`, and rendered `scrape` can use a Docker-hosted Obscura CDP server from `PIGO_OBSCURA_URL` or `OBSCURA_URL`. See [Configuration](docs/configuration.md#research-tools).

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
