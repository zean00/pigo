# A2A

`pigo` includes optional Agent-to-Agent Protocol support for interoperability with other agent runtimes.

A2A is separate from ACP. ACP remains the main client-to-agent interface for host applications that control a pigo session. A2A is for agent-to-agent delegation: pigo can serve itself as a remote agent and can expose configured remote A2A agents as model tools.

The implementation targets A2A JSON-RPC over HTTP with Agent Card discovery and SSE streaming for task lifecycle updates.

## Serving pigo over A2A

Start an A2A HTTP server:

```bash
go run ./cmd/pigo-a2a --addr 127.0.0.1:4388 --provider openai --model gpt-4.1-mini
```

Useful flags:

- `--addr`: HTTP listen address.
- `--base-url`: public base URL used in the Agent Card.
- `--cwd`: workspace root for new tasks.
- `--provider` and `--model`: default model selection for new tasks.
- `--name` and `--description`: Agent Card identity fields.
- `--bearer-token`: optional bearer token required for `/a2a`. It can also be set with `PIGO_A2A_BEARER_TOKEN`.

The server exposes:

- `GET /.well-known/agent-card.json`
- `POST /a2a`

Supported JSON-RPC methods:

- `message/send`
- `message/stream`
- `tasks/get`
- `tasks/cancel`

The Agent Card advertises JSON-RPC transport, text input/output, streaming, and the default coding-agent skill.

## Calling Remote A2A Agents

Remote A2A tools are disabled by default. Enable them with config:

```bash
export PIGO_A2A_TOOLS=on
export PIGO_A2A_CONFIG_JSON='{
  "enabled": true,
  "agents": [
    {
      "name": "research",
      "url": "http://localhost:4388/a2a",
      "allowInsecure": true
    }
  ]
}'
```

Config file discovery uses:

1. `PIGO_A2A_CONFIG_JSON`
2. `PIGO_A2A_CONFIG`
3. `<workspace>/.pi/a2a.json`
4. `~/.pi/agent/a2a.json`

Each configured agent is exposed as:

```text
a2a__<agent>__send_message
```

The tool accepts:

- `message`: task message for the remote agent.
- `taskId`: optional existing task id to continue.
- `contextId`: optional context id.
- `skill`: optional remote skill hint.
- `blocking`: whether to wait for completion; defaults to `true`.

When a remote Agent Card advertises streaming, pigo uses `message/stream`, folds task lifecycle events into the returned task state, and forwards progress as partial tool-result updates. Otherwise it uses `message/send`.

## Config Options

ACP/RPC config options:

- `a2a_tools`: `on` or `off`.
- `a2a_agents`: comma-separated `name=url` shorthand for simple local setups.

JSONL RPC commands:

- `set_a2a_tools`
- `get_a2a_agents`

## Security

Remote A2A agents are treated as untrusted external sources. The optional prompt-injection guard includes `a2a` in its default source list and treats `a2a__*` tools as sensitive in `enforce` mode.

Client-side config only sends explicitly configured headers or bearer tokens. HTTP URLs are accepted only for localhost/test endpoints unless the agent config sets `allowInsecure`.

## Current Boundaries

This is an interoperability layer, not a built-in orchestrator. pigo can call remote agents as tools and can serve task requests to other agents. Higher-level routing, supervisor policies, task decomposition, and multi-agent workflow state should live in a host application or a future optional module.
