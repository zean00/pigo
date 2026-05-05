# Architecture

`pigo` is organized around a small set of runtime layers. The layers are designed so another project can launch or embed the headless coding agent and communicate with it through ACP.

## Layer Overview

| Layer | Package | Responsibility |
| --- | --- | --- |
| Provider runtime | `pkg/ai` | Model catalog, provider transports, normalized messages/events, auth, OAuth, streaming, tool calls, and usage. |
| Agent loop | `pkg/agentcore` | Prompt loop, system prompt handling, tool execution, event emission, session state, and provider request construction. |
| Coding-agent runtime | `pkg/codingagent` | Workspace tools, session entries, branching, labels, hooks, JSONL RPC, OAuth state, model selection, and headless coding behavior. |
| ACP adapter | `pkg/acpadapter` | JSON-RPC stdio ACP server and ACP session lifecycle mapping. |
| MCP adapter | `pkg/mcpadapter` | MCP server config loading, client registry, tool discovery, tool invocation, and progress forwarding. |
| Commands | `cmd/*` | CLI entry points for ACP, RPC, auth, conformance, parity, and model generation. |

## Runtime Boundaries

### `pkg/ai`

The provider layer normalizes provider-specific APIs into common messages, content blocks, normalized results, streaming events, usage, and tool calls. It contains transports for OpenAI-compatible APIs, Anthropic-compatible APIs, Mistral, Google, Google Vertex, Google Gemini CLI-style endpoints, OpenAI Codex, and Amazon Bedrock.

### `pkg/agentcore`

The agent core owns the generic model/tool loop. It converts session messages into provider messages, sends completion requests, streams events, executes tools requested by the model, records tool results, and continues until the model stops or the caller cancels.

### `pkg/codingagent`

The coding-agent runtime adds workspace behavior. It provides read, edit, grep/search, bash, session persistence, branch and label handling, hooks, model/mode selection, and JSONL RPC commands. This is the main headless coding agent target.

The runtime also owns an internal session module registry. Built-in capabilities such as command-output compression, bash permissions, built-in tool filtering, research tools, usage quotas, extension tools, and core session metadata register through this registry instead of requiring direct changes to every runtime surface. Modules can contribute:

- Model-facing tools and tool specs.
- ACP/RPC config options and setters.
- JSONL RPC handlers.
- Session-entry policies for tree visibility, branch leaf behavior, state rebuild, and export metadata.

Module registration is atomic. If a module returns an error while registering capabilities, any partially installed tools, config options, RPC handlers, or entry handlers are rolled back so the session is not left in a mixed state and the module can be retried.

### `pkg/acpadapter`

The ACP adapter exposes the coding-agent runtime through the Agent Client Protocol. It translates ACP session requests into coding-agent operations and translates runtime events back into ACP `session/update` notifications.

### `pkg/mcpadapter`

The MCP adapter loads MCP server definitions, connects to stdio/HTTP/SSE servers, discovers tool schemas, exposes those tools to the agent loop, invokes them, and forwards progress notifications back through ACP.

## Data Flow

```text
ACP client or RPC caller
        |
        v
cmd/pigo-acp or cmd/pigo-rpc
        |
        v
pkg/acpadapter or pkg/codingagent.RPCServer
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

Tool calls travel back through `pkg/agentcore` into coding-agent tools or MCP tools. Tool results are normalized and appended to the conversation before the loop continues.

## Persistence

Session history is stored as JSONL entries when a session store is configured. The store records conversation state and metadata needed to restore the active branch. OAuth secrets are excluded from normal session entries and should be stored through the dedicated OAuth store.
