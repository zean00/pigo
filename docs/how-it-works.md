# How It Works

`pigo` runs a headless coding-agent session around a workspace root. A client sends prompts, the agent calls a model provider, the model may request tools, and the runtime applies tool calls against the workspace or external MCP servers.

## ACP Session Flow

1. The host process starts `pigo-acp`.
2. The ACP client sends `initialize`.
3. The client creates or loads a session with `session/new`, `session/load`, `session/resume`, or `session/fork`.
4. The client sends user input with `session/prompt`.
5. `pigo` streams `session/update` notifications for assistant text, tool calls, tool results, errors, and completion.
6. The client may cancel with `session/cancel` or close with `session/close`.

## Agent Loop

For each prompt:

1. The session builds the current conversation state.
2. The agent core applies the configured system prompt and available tools.
3. The provider layer converts normalized messages into provider-specific request payloads.
4. Streaming provider events are normalized into common event types.
5. Tool calls are routed to coding-agent tools or MCP tools.
6. Tool results are appended to the model conversation.
7. The loop continues until the provider returns a final assistant response or the request is canceled.

## Session Purpose

The default session purpose is `coding`, which preserves the normal headless coding-agent system prompt. Sessions can also use `generic`, `research`, or `readonly` purpose to reduce coding-specific prompt framing for embedders that use pigo as a broader headless agent.

Purpose affects only the composed system prompt and context sections. It does not change tool availability. A read-only or research-style session should still use built-in tool policy, bash permissions, research tool settings, and MCP configuration to expose the intended tool surface.

The system prompt is composed from the purpose prompt, workspace path, optional git/package context, configured context files, extra embedder instructions, and the active agent profile overlay.

## Workspace Tools

The coding-agent runtime provides tools for common headless coding tasks:

- Reading files.
- Editing files.
- Searching with grep-style behavior.
- Running shell commands.
- Discovering workspace files.
- Tracking session state, branches, labels, and metadata.

Workspace paths are resolved against the session root to avoid unintended access outside the workspace.

The model-facing built-in tool names are `bash`, `write`, `read`, `edit`, `ls`, `grep`, and `find`. Sessions can filter these built-in tools before the provider request is built. An enabled list exposes only selected built-ins; a disabled list removes tools from the exposed set. Extension tools and MCP tools are appended separately.

Optional research tools are also appended separately when enabled. `research` runs an isolated quick-mode sub-agent, `search` talks to an external SearXNG instance, `scrape` fetches and extracts readable URL text with static HTTP or an optional Docker-hosted Obscura CDP server, and `security_search` queries public vulnerability sources. They are opt-in so ordinary headless coding sessions do not gain network research tools unexpectedly.

There is no separate research-specific grep tool. The built-in `grep` is the canonical local search tool and returns structured match metadata that future research orchestration can count under its gathering budget.

## Internal Modules

Session capabilities are registered through an internal module registry. The registry keeps optional features from spreading hardcoded branches across the core runtime, ACP adapter, and JSONL RPC server.

Current internal modules register core config, command-output compression, bash permissions, built-in tool policy, built-in tools, research tools, extension tools, agent profile selection, optional tool discovery, and usage quota/ledger behavior. The same registered config option drives ACP `session/set_config_option`, ACP session state, and JSONL RPC behavior where applicable.

Session-entry modules define whether metadata appears in the branch tree, whether it advances the active leaf, how it is reapplied after branching, and how it is exported. This is why labels and usage metadata can participate in persistence without becoming visible conversation nodes.

Module registration is transactional. A module that fails during registration is rolled back before the error is returned.

## Agent Profiles And Workflows

`pigo` can load lightweight agent resources inspired by `agent-pi` without implementing a full multi-agent runtime. Agent profiles are markdown files under `~/.pi/agent/agents` or `<workspace>/.pi/agents`; frontmatter can define `name`, `description`, `provider`, `model`, `thinkingLevel`, and comma-separated `tools`, while the markdown body is used as profile instructions.

Selecting an `agent_profile` applies the profile instructions as a system-prompt overlay. If the profile declares a provider/model or thinking level, those values are applied to the session. Selecting `default` clears the profile overlay.

Teams and chains are loaded from `teams.yaml` and `chains.yaml` in the same agent resource directories and are exposed as metadata to ACP/RPC clients. pigo does not execute teams or chains by itself; host applications can use the metadata to orchestrate multiple sessions or future modules can build on it.

The optional `tool_search` tool is a read-only discovery tool. When enabled, it returns visible tool names, descriptions, and source categories. It does not invoke other tools.

## Tool Execution Modes

The agent loop supports three tool execution modes:

- `parallel`: execute all tool calls returned by one assistant turn concurrently and return the full batch of results to the model. This is the default.
- `sequential`: execute all tool calls returned by one assistant turn in order, then return the full batch of results to the model.
- `interleaved`: execute at most one tool call from each assistant turn, append that result, then call the model again so it can reason before choosing the next tool.

Interleaved mode also asks OpenAI-compatible providers to disable provider-side parallel tool calls when that request option is supported. If a provider still returns or streams multiple tool calls in one turn, `pigo` exposes only the first call in assistant message events and conversation history, then lets the model request additional calls on later rounds.

## Command Output Compression

Bash command output passes through a command-aware compression layer after the process exits. The layer is conservative: it preserves the exit code and cancellation status, and it does not rewrite the command before execution.

The runtime chooses a matching filter for common noisy commands such as `go test`, `git diff`, `git status`, `rg`, `grep`, `ls`, and `find`. If no command-specific filter applies, the generic fallback enforces the configured output size. The existing tool output limit remains the final hard cap.

Compression metadata is attached to bash tool results and direct bash results so ACP/RPC clients can see whether output was compressed and which filter produced it.

The committed test suite includes deterministic RPC/session coverage and an optional live OpenRouter smoke test. The live test is useful when changing provider/tool-loop behavior because it validates that a real model-triggered bash call still receives compressed output metadata.

## Bash Command Permissions

Before a bash command is executed, `pigo` evaluates the session bash permission policy. The policy supports exact, glob, and regex rules. In `allow-all` mode, commands run unless denied. In `allow-list` mode, commands must match an allow rule and must not match any deny rule.

Deny rules always win. A denied command returns a permission-denied bash/tool result without launching a shell process.

## Event Mapping

Internal runtime events are normalized before being sent to clients. ACP clients receive `session/update` notifications for:

- Assistant message chunks.
- Tool call lifecycle updates.
- Tool call output.
- Diagnostics and errors.
- Session lifecycle changes.

Streaming assistant chunks use stable message identifiers so ACP clients can append chunks to the same visible message.

## Provider Selection

The selected model determines the provider transport. `pigo` includes a generated model catalog and provider specs. It can also register custom provider configs at runtime for OpenAI-compatible services, including local LLM servers.

## Cancellation

ACP `session/cancel` cancels an active prompt context. Provider calls and tool execution paths receive context cancellation where supported, and the session emits cancellation-aware events.

## Validation

Behavior is checked with:

- Go unit tests: `go test ./...`
- Fixture conformance: `make verify-conformance`
- Cross-repo parity comparator: `make parity`
- Optional live OpenRouter smoke test: `OPENROUTER_API_KEY=... make live-openrouter-smoke`
