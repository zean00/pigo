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
