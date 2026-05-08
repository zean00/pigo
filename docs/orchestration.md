# Orchestration

`pigo` can optionally use A2A agents for lightweight supervisor and task-graph orchestration. This is an extension layer, not a change to the core model/tool loop.

The orchestrator is disabled by default and depends on configured A2A agents.

## Enable

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
export PIGO_ORCHESTRATOR=on
export PIGO_ORCHESTRATOR_MAX_PARALLEL=3
export PIGO_ORCHESTRATOR_TIMEOUT_MS=120000
export PIGO_ORCHESTRATOR_AGENTS=research
```

## Model Tools

When enabled, the session exposes:

- `delegate_task`: send one task to a selected or auto-routed A2A agent.
- `orchestrate_task`: run a bounded task graph against A2A agents and return a Markdown result.
- `orchestration_status`: inspect a run by id.
- `cancel_orchestration`: cancel an active run by id.

`delegate_task` accepts `message`, optional `agent`, and optional `skill`.

`orchestrate_task` accepts `goal`, optional `steps`, optional `maxParallel`, and optional `timeoutMillis`. Each step can include `id`, `agent`, `skill`, `message`, `dependsOn`, and `optional`.

## ACP and RPC

ACP sessions expose these config options:

- `orchestrator_enabled`
- `orchestrator_max_parallel`
- `orchestrator_timeout_ms`
- `orchestrator_agents`
- `orchestrator_reducer`

JSONL RPC supports:

- `start_orchestration`
- `get_orchestration`
- `list_orchestrations`
- `cancel_orchestration`

Orchestration summaries are also included in ACP session state.

## Behavior

The orchestrator builds its registry from configured A2A agents plus loaded agent profiles. Explicit agent selection wins. Otherwise, routing uses simple name, description, skill, and tag matching.

Runs are stored as session custom entries with `customType: "orchestrator.run"`. They can be listed or inspected later through RPC and ACP state.

The default reducer is deterministic Markdown aggregation. It preserves each remote agent result under its source agent/task heading.

## Boundaries

The orchestrator calls remote agents through A2A only. It does not modify `pkg/agentcore`, `pkg/ai`, provider transports, or built-in workspace tools. MCP remains a tool attachment layer, not an agent orchestration protocol.
