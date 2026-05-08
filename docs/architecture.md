# Architecture

`pigo` is organized around a small set of runtime layers. The layers are designed so another project can launch or embed the headless coding agent and communicate with it through ACP.

## Layer Overview

| Layer | Package | Responsibility |
| --- | --- | --- |
| Provider runtime | `pkg/ai` | Model catalog, provider transports, normalized messages/events, auth, OAuth, streaming, tool calls, and usage. |
| Agent loop | `pkg/agentcore` | Prompt loop, system prompt handling, tool execution, event emission, session state, and provider request construction. |
| Coding-agent runtime | `pkg/codingagent` | Workspace tools, session entries, branching, labels, hooks, JSONL RPC, OAuth state, model selection, and headless coding behavior. |
| ACP adapter | `pkg/acpadapter` | JSON-RPC stdio ACP server and ACP session lifecycle mapping. |
| A2A protocol/client | `pkg/a2a` | A2A protocol objects, Agent Card client, JSON-RPC/SSE client, config loading, and remote-agent model tools. |
| A2A adapter | `pkg/a2aadapter` | HTTP A2A server that exposes pigo sessions as remote A2A tasks. |
| Orchestrator | `pkg/orchestrator` | Optional A2A-backed supervisor delegation, task graph execution, and result reduction. |
| MCP adapter | `pkg/mcpadapter` | MCP server config loading, client registry, tool discovery, tool invocation, and progress forwarding. |
| Commands | `cmd/*` | CLI entry points for ACP, RPC, auth, conformance, parity, and model generation. |

## Runtime Boundaries

### `pkg/ai`

The provider layer normalizes provider-specific APIs into common messages, content blocks, normalized results, streaming events, usage, and tool calls. It contains transports for OpenAI-compatible APIs, Anthropic-compatible APIs, Mistral, Google, Google Vertex, Google Gemini CLI-style endpoints, OpenAI Codex, and Amazon Bedrock.

### `pkg/agentcore`

The agent core owns the generic model/tool loop. It converts session messages into provider messages, sends completion requests, streams events, executes tools requested by the model, records tool results, and continues until the model stops or the caller cancels.

### `pkg/codingagent`

The coding-agent runtime adds workspace behavior. It provides read, edit, grep/search, bash, session persistence, branch and label handling, hooks, model/mode selection, configurable session-purpose prompting, and JSONL RPC commands. This is the main headless coding agent target.

The runtime also owns an internal session module registry. Built-in capabilities such as session purpose/context config, prompt-injection guard config, command-output compression, bash permissions, built-in tool filtering, A2A remote-agent tools, optional orchestration, research tools, agent profile selection, usage quotas, extension tools, and core session metadata register through this registry instead of requiring direct changes to every runtime surface. Modules can contribute:

- Model-facing tools and tool specs.
- ACP/RPC config options and setters.
- JSONL RPC handlers.
- Session-entry policies for tree visibility, branch leaf behavior, state rebuild, and export metadata.

Module registration is atomic. If a module returns an error while registering capabilities, any partially installed tools, config options, RPC handlers, or entry handlers are rolled back so the session is not left in a mixed state and the module can be retried.

Agent profiles, teams, and chains are treated as headless resources. They are loaded from `.pi/agents` and `~/.pi/agent/agents`, exposed to ACP/RPC clients, and can be used as prompt/config overlays. They are intentionally metadata and configuration in pigo itself; full supervisor/worker orchestration belongs in a host application or a future module.

### `pkg/acpadapter`

The ACP adapter exposes the coding-agent runtime through the Agent Client Protocol. It translates ACP session requests into coding-agent operations and translates runtime events back into ACP `session/update` notifications.

### `pkg/mcpadapter`

The MCP adapter loads MCP server definitions, connects to stdio/HTTP/SSE servers, discovers tool schemas, exposes those tools to the agent loop, invokes them, and forwards progress notifications back through ACP.

### `pkg/a2a` and `pkg/a2aadapter`

The A2A packages add an agent-to-agent interoperability boundary without turning the core runtime into an orchestrator.

`pkg/a2aadapter` serves pigo over HTTP. It publishes an Agent Card at `/.well-known/agent-card.json`, accepts JSON-RPC requests at `/a2a`, maps A2A messages to coding-agent prompts, stores in-memory task state, and returns A2A task/artifact/status objects.

`pkg/a2a` also acts as a client. When enabled, configured remote A2A agents are exposed as model tools named `a2a__<agent>__send_message`. Those tools fetch the remote Agent Card, call `message/stream` when streaming is advertised, fall back to `message/send`, and return the final task text plus task metadata.

### `pkg/orchestrator`

The orchestrator is an optional layer above A2A. It provides supervisor-style delegation and bounded task-graph execution using configured A2A agents. It does not call providers directly and does not modify `pkg/agentcore` or `pkg/ai`.

The coding-agent module exposes orchestrator tools and RPC/config handlers when enabled. Run snapshots are persisted as custom session entries so ACP/RPC clients can inspect orchestration state without a new database or session format.

## Data Flow

```text
ACP client, A2A client, or RPC caller
        |
        v
cmd/pigo-acp, cmd/pigo-a2a, or cmd/pigo-rpc
        |
        v
pkg/acpadapter, pkg/a2aadapter, or pkg/codingagent.RPCServer
        |
        v
pkg/codingagent.Session
        |
        v
pkg/agentcore loop
        |
        v
pkg/ai provider transport
        |
        v
model provider or local OpenAI-compatible server
```

Tool calls travel back through `pkg/agentcore` into coding-agent tools, MCP tools, A2A remote-agent tools, or extension tools. Tool results are normalized and appended to the conversation before the loop continues.

## Persistence

Session history is stored as JSONL entries when a session store is configured. The store records conversation state and metadata needed to restore the active branch. OAuth secrets are excluded from normal session entries and should be stored through the dedicated OAuth store.
