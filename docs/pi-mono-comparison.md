# Comparison with original pi-mono

`pigo` is not a full rewrite of the entire `pi-mono` product. It is a Go porting effort for the runtime pieces needed to provide a headless coding agent that another project can use through ACP.

## Primary Difference

| Area | `pi-mono` | `pigo` |
| --- | --- | --- |
| Language | TypeScript | Go |
| Product surface | Includes interactive/product packages | Focused on headless runtime and adapters |
| Main integration target | Native `pi-mono` app/runtime | External projects through ACP |
| Tool extension | TypeScript runtime integrations | Built-in coding tools plus MCP adapter |
| Validation | Source of behavior and fixtures | Conformance and parity against `pi-mono` |

## Ported Runtime Areas

- Provider-independent message and event contracts.
- Provider catalog and provider transports.
- Streaming text, reasoning, tool calls, tool results, and usage normalization.
- Generic agent loop behavior.
- Headless coding-agent session behavior.
- Workspace read/edit/search/bash behavior.
- Session persistence, branch, label, resume, load, and fork behavior.
- OAuth provider/store behavior for supported providers.
- ACP adapter for protocol-facing lifecycle.
- MCP adapter for external tools.

## Intentionally Out of Scope

- TUI implementation.
- Web UI or app-specific UI surfaces.
- TypeScript package ergonomics.
- Product-specific workflows that do not affect the headless coding-agent runtime.

## Compatibility Strategy

`pigo` uses fixtures and comparators from the original project to verify comparable runtime behavior. The goal is not identical internal implementation; the goal is compatible behavior at the provider, agent loop, coding-agent, ACP, and MCP boundaries relevant to headless operation.

Run fixture conformance:

```bash
make verify-conformance
```

Run cross-repo parity:

```bash
make parity
```

The parity command expects `pi-mono` to be available at `../pi-mono`.

## Status Model

The project tracks parity work in `PI_MONO_PARITY_TRACKER.md`. That tracker is the working implementation checklist for runtime compatibility with `pi-mono`.
