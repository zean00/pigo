# Overview

`pigo` is a Go porting effort for selected parts of the original `pi-mono` project.

The purpose of this port is to create a headless coding agent runtime that can be embedded in, launched by, or controlled from another project. The main integration boundary is ACP, the Agent Client Protocol. `pigo` exposes the coding-agent session lifecycle over ACP and can attach external MCP tools to each session.

The project is intentionally focused on runtime behavior rather than recreating every interactive product surface from `pi-mono`.

The default session purpose is still `coding`, but embedders can configure `generic`, `research`, or `readonly` prompt framing when they want to use the same headless runtime outside a strictly coding-oriented workflow. Tool access remains controlled separately by tool policy, bash permissions, research-tool settings, MCP tools, and ACP/RPC config.

## Goals

- Provide a reusable headless coding agent for local workspaces.
- Communicate with host applications through ACP.
- Attach MCP servers and expose their tools to the model.
- Preserve comparable behavior with the relevant `pi-mono` provider, agent-core, and headless coding-agent paths.
- Keep the implementation Go-native for services, CLIs, and projects that want to embed or supervise the agent.

## Current Scope

- `pkg/ai`: provider-independent message, stream, tool-call, model, auth, usage, and normalization contracts.
- `pkg/agentcore`: generic agent loop, tool execution, event streaming, session state, and provider loop behavior.
- `pkg/codingagent`: headless coding-agent runtime for workspace operations, session storage, RPC, hooks, OAuth, and model selection.
- `pkg/acpadapter`: ACP stdio server that exposes `pigo` sessions to ACP clients.
- `pkg/mcpadapter`: MCP client registry and tool bridge for stdio, HTTP, and SSE MCP servers.

## Non-Goals

- Rebuilding the `pi-mono` TUI.
- Rebuilding web UI or product-specific interactive surfaces.
- Recreating TypeScript SDK ergonomics where they do not matter to the Go headless runtime.

## Command Surface

- `cmd/pigo-acp`: ACP stdio adapter.
- `cmd/pigo-rpc`: minimal JSONL RPC server for the headless coding-agent runtime.
- `cmd/pigo-auth`: OAuth helper for supported OAuth providers.
- `cmd/pigo-parity`: behavior comparator against a local `pi-mono` checkout.
- `cmd/pigo-*-conformance`: fixture-driven conformance command runners.
- `cmd/pigo-generate-models`: generated model catalog refresh tool.

## Documentation Map

- [Installation](installation.md): prerequisites and setup.
- [Configuration](configuration.md): providers, auth, local LLMs, sessions, and MCP config.
- [Architecture](architecture.md): packages and runtime boundaries.
- [How It Works](how-it-works.md): request flow from ACP/RPC through model and tools.
- [Comparison with pi-mono](pi-mono-comparison.md): what is ported, comparable, and intentionally omitted.
- [ACP](acp.md): Agent Client Protocol support.
- [MCP](mcp.md): Model Context Protocol tool support.
