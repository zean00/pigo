# `pi-mono` vs `pigo` Implementation Tracker

Last rescan: 2026-04-30

This document is a codebase-based tracker, not a conversational summary. It was written after rescanning:

- upstream TypeScript:
  - `pi-mono/packages/ai`
  - `pi-mono/packages/coding-agent/src/core`
  - `pi-mono/packages/coding-agent` runtime/session/RPC surfaces that overlap the Go headless target
- Go port:
  - `pigo/pkg/ai`
  - `pigo/pkg/agentcore`
  - `pigo/pkg/codingagent`

## Legend

- `DONE`: implemented in `pigo` with comparable runtime behavior
- `PARTIAL`: present, but narrower than `pi-mono`, or missing some important edge cases
- `MISSING`: not implemented in `pigo`
- `OOS`: intentionally out of scope for the current Go target, especially interactive/UI-only TypeScript surfaces

## Package Mapping

There is no standalone `packages/agent-core` package in `pi-mono`.

For comparison purposes, the mapping is:

- `pi-mono/packages/ai` -> `pigo/pkg/ai`
- `pi-mono/packages/coding-agent/src/core/*` -> split across:
  - `pigo/pkg/agentcore` for the generic agent loop/event/tool core
  - `pigo/pkg/codingagent` for session/runtime/headless coding-agent behavior
- `pi-mono/packages/coding-agent` interactive/TUI/UI/product surface -> mostly `OOS` for the current Go headless target

## Size Snapshot

This is not a quality metric, but it is a useful sanity check for scope.

- `pi-mono/packages/ai`: 416 files
- `pi-mono/packages/coding-agent/src/core`: 62 files
- `pigo/pkg/ai`: 57 files
- `pigo/pkg/agentcore`: 10 files
- `pigo/pkg/codingagent`: 9 files

## High-Level Status

### `ai`

Status: `PARTIAL`, but close on the main execution path.

The core provider runtime is now strong enough to be compared directly with `pi-mono` for many real workloads, especially around OpenAI-compatible providers. Remaining gaps are mostly long-tail provider quirks and exact event/API-shape fidelity.

### `agentcore`

Status: `PARTIAL`.

The generic loop/tool/event core exists and is usable. It covers the main headless agent loop well, but it is still much smaller than the total `coding-agent/src/core` surface in TypeScript.

### `codingagent` (headless target)

Status: `PARTIAL`.

There is a usable headless session/runtime/RPC layer. The largest remaining gaps are higher-level runtime/product behaviors from the TypeScript package, especially extensions/SDK richness and non-headless workflows.

## Detailed Tracker: `ai`

Evidence in `pigo`:

- `pkg/ai/openai.go`
- `pkg/ai/openai_codex.go`
- `pkg/ai/anthropic.go`
- `pkg/ai/google.go`
- `pkg/ai/google_gemini_cli.go`
- `pkg/ai/google_vertex.go`
- `pkg/ai/bedrock.go`
- `pkg/ai/mistral.go`
- `pkg/ai/transform_messages.go`
- `pkg/ai/provider_catalog.go`
- `pkg/ai/models.generated.json`
- tests under `pkg/ai/*_test.go`

Evidence in `pi-mono`:

- `packages/ai/src/providers/*`
- `packages/ai/src/models.ts`
- `packages/ai/src/models.generated.ts`
- `packages/ai/src/providers/transform-messages.ts`
- `packages/ai/src/utils/oauth/*`
- `packages/ai/test/*`

### Core registry, models, auth, config

- `DONE` provider registry and built-in provider dispatch
  - `pigo`: `pkg/ai/registry.go`, `pkg/ai/provider_catalog.go`
  - `pi-mono`: `packages/ai/src/providers/register-builtins.ts`, `packages/ai/src/api-registry.ts`

- `DONE` runtime model catalog loading
  - `pigo`: `pkg/ai/models.go`, `pkg/ai/models.generated.json`
  - `pi-mono`: `packages/ai/src/models.ts`, `packages/ai/src/models.generated.ts`

- `PARTIAL` catalog workflow parity
  - `pigo` has runtime catalog data, but not the same TS-first generator/update workflow
  - `pi-mono`: `packages/ai/scripts/generate-models.ts`
  - this is not operationally critical if `pigo` is no longer intended to sync from `pi-mono`

- `DONE` provider config and request-time option hooks
  - `pigo`: `pkg/ai/provider_config.go`, `pkg/ai/hooks.go`
  - includes `OnPayload`, `OnResponse`, custom headers, retry knobs

- `PARTIAL` OAuth/auth integration breadth
  - `pigo`: `pkg/ai/oauth.go`, `pkg/ai/oauth_store.go`, `pkg/ai/auth.go`
  - `pi-mono` has broader TS app-facing OAuth helpers and provider-specific auth mutation flows

### Provider coverage

- `DONE` OpenAI `chat/completions`
  - `pigo`: `pkg/ai/openai.go`
  - request shaping, streaming, usage parsing, replay normalization are all present

- `DONE` OpenAI `responses`
  - `pigo`: `pkg/ai/openai.go`
  - non-stream and stream paths exist

- `DONE` OpenAI Codex responses
  - `pigo`: `pkg/ai/openai_codex.go`
  - SSE and websocket transport support are present

- `DONE` Anthropic
  - `pigo`: `pkg/ai/anthropic.go`

- `DONE` Google Gemini API
  - `pigo`: `pkg/ai/google.go`

- `DONE` Google Gemini CLI / Antigravity style provider
  - `pigo`: `pkg/ai/google_gemini_cli.go`

- `DONE` Google Vertex
  - `pigo`: `pkg/ai/google_vertex.go`

- `DONE` Amazon Bedrock
  - `pigo`: `pkg/ai/bedrock.go`

- `DONE` Mistral
  - `pigo`: `pkg/ai/mistral.go`

- `DONE` unsupported/scripted/testing surfaces
  - `pigo`: `pkg/ai/scripted.go`, `pkg/ai/unsupported.go`

- `PARTIAL` provider fidelity across all provider-specific edge cases
  - broad provider presence exists
  - remaining gaps are mostly per-provider nuance rather than missing provider files

### Message transformation and replay normalization

- `DONE` pre-provider message transformation
  - `pigo`: `pkg/ai/transform_messages.go`
  - `pi-mono`: `packages/ai/src/providers/transform-messages.ts`

- `DONE` tool-call ID normalization for replay/cross-provider handoff
  - `pigo`: `pkg/ai/transform_messages.go`, `pkg/ai/openai.go`

- `DONE` orphaned tool-result synthesis / replay cleanup
  - implemented in the transform layer

- `DONE` metadata preservation through replay normalization
  - provider/api/model fields are preserved through the Go path now

### OpenAI-compatible compat shaping

- `DONE` reasoning-effort and thinking-mode shaping
  - DeepSeek, OpenRouter, ZAI, Groq/Qwen mapping are implemented
  - `pigo`: `pkg/ai/openai.go`

- `DONE` developer-role vs system-role compatibility

- `DONE` prompt cache / cache retention / session affinity handling

- `DONE` OpenRouter and Vercel routing hints

- `DONE` Copilot request header shaping

- `DONE` empty `tools: []` fallback for tool-history replay cases

- `PARTIAL` long-tail compat auto-detection breadth
  - important operational cases are covered
  - TypeScript still has a richer accumulated provider-specific compatibility surface

### Streaming, usage, ordering, and errors

- `DONE` streamed OpenAI reasoning/text/tool-call parsing

- `DONE` streamed block ordering preservation
  - `pigo` now preserves interleaving order instead of flattening to thinking/text/tools

- `DONE` streamed usage parsing for both:
  - `chunk.usage`
  - `choice.usage` fallback

- `DONE` usage cost calculation on OpenAI paths
  - chat completions, responses, streamed and non-streamed

- `DONE` structured HTTP error extraction
  - includes OpenAI-style structured error message and raw metadata where present

- `DONE` oversized SSE line handling on OpenAI chat-completions stream parser

- `DONE` Responses stream tool-call ID fidelity on `toolcall_end`

- `PARTIAL` exact event object shape parity
  - normalized events are functionally close
  - they are not guaranteed to be byte-for-byte or TS-API-shape identical

### Remaining `ai` gaps

- `PARTIAL` non-OpenAI provider edge-case fidelity
  - Anthropic
  - Google
  - Bedrock
  - Mistral
  - other provider-specific quirks already encoded in TS tests

- `PARTIAL` exact TS app-side auth/model mutation hooks

- `PARTIAL` exact stream event ordering/payload edge cases beyond the normalized behavior already ported

## Detailed Tracker: `agentcore`

Evidence in `pigo`:

- `pkg/agentcore/agent.go`
- `pkg/agentcore/loop.go`
- `pkg/agentcore/stream.go`
- `pkg/agentcore/events.go`
- `pkg/agentcore/proxy.go`
- tests under `pkg/agentcore/*_test.go`

Upstream comparison surface in `pi-mono`:

- `packages/coding-agent/src/core/agent-session.ts`
- `packages/coding-agent/src/core/messages.ts`
- `packages/coding-agent/src/core/event-bus.ts`
- `packages/coding-agent/src/core/tools/*`
- selected runtime/session behaviors from `packages/coding-agent/src/core/*`

### Generic agent loop/runtime

- `DONE` agent state container and prompt/continue loop wrapper
  - `pigo`: `pkg/agentcore/agent.go`

- `DONE` scripted loop
  - `pigo`: `pkg/agentcore/loop.go`

- `DONE` provider loop with tool execution
  - `pigo`: `pkg/agentcore/loop.go`

- `DONE` stream wrappers for provider loop and agent loop
  - `pigo`: `pkg/agentcore/stream.go`

- `DONE` steering and follow-up queue handling
  - `pigo`: `pkg/agentcore/agent.go`, `pkg/agentcore/loop.go`

- `DONE` before/after tool hooks
  - `pigo`: `pkg/agentcore/loop.go`

- `DONE` structured prompt/image handling through the generic core

- `DONE` state/message/provider/model metadata preservation

### Event surface

- `DONE` normalized event stream for:
  - turn start/end
  - assistant deltas
  - tool execution start/end
  - tool result emission

- `PARTIAL` exact parity with TS event surface
  - Go exposes a narrower event API than the full TS app/runtime event surface

### Proxy/stream surface

- `PARTIAL` proxy support exists
  - `pigo`: `pkg/agentcore/proxy.go`

- `PARTIAL` exact TS proxy stream surface parity
  - still not a 1:1 mirror of TS behavior/API shape

### Remaining `agentcore` gaps

- `MISSING` full equivalent of the higher-level `AgentSession` abstraction
  - much of that behavior currently lives in `pkg/codingagent`, not `pkg/agentcore`

- `MISSING` TS-style extensible custom message typing
  - not a natural Go 1:1 target

- `PARTIAL` parity with broader runtime/session orchestration semantics from TypeScript

## Detailed Tracker: `codingagent` (headless target)

Evidence in `pigo`:

- `pkg/codingagent/runtime.go`
- `pkg/codingagent/session.go`
- `pkg/codingagent/session_store.go`
- `pkg/codingagent/resources.go`
- `pkg/codingagent/rpc.go`
- tests under `pkg/codingagent/*_test.go`

Upstream comparison surface in `pi-mono`:

- `packages/coding-agent/src/core/agent-session.ts`
- `packages/coding-agent/src/core/session-manager.ts`
- `packages/coding-agent/src/core/resource-loader.ts`
- `packages/coding-agent/src/core/skills.ts`
- `packages/coding-agent/src/core/prompt-templates.ts`
- `packages/coding-agent/src/core/compaction/*`
- `packages/coding-agent/src/modes/rpc/*`
- plus tests under `packages/coding-agent/test/*` and `test/suite/*`

### Headless session runtime

- `DONE` headless session type and prompt flow
  - `pigo`: `pkg/codingagent/runtime.go`

- `DONE` prompt / steer / follow-up / retry / abort

- `DONE` provider/model/thinking-level selection

- `DONE` auto retry and auto compaction toggles

- `DONE` built-in tool set for headless target
  - `bash`
  - `write`
  - `read`
  - `edit`
  - `ls`
  - `grep`
  - `find`

- `PARTIAL` built-in tool parity with TypeScript core tool stack
  - TS has a larger tool-supporting surface around rendering, path utilities, file mutation queue, diff helpers, and UI-linked behaviors

### Session persistence, tree, branch, export, share

- `DONE` session store and reloadable session entries
  - `pigo`: `pkg/codingagent/session_store.go`, `pkg/codingagent/runtime.go`

- `DONE` tree model, labels, branch, fork, switch session

- `DONE` HTML export

- `DONE` JSONL export

- `DONE` share output path surface

- `PARTIAL` parity with TypeScript session manager behavior
  - core flows exist
  - deeper TS session manager semantics and edge cases are broader

### Compaction

- `DONE` manual compaction path

- `DONE` auto-compaction trigger path

- `PARTIAL` richer compaction workflow parity
  - TS has more nuanced branch summarization, abort handling, context estimation, extension hooks, and compaction-related session events

### Resources, slash commands, prompt templates, skills

- `DONE` resource loading for slash command style resources
  - `pigo`: `pkg/codingagent/resources.go`

- `DONE` prompt template and skill registration surfaces in the headless session runtime

- `PARTIAL` skills/extensions parity
  - `pigo` has session-facing slots for extension commands, prompt templates, and skills
  - it does not yet mirror the full TS extension runtime/loader/wrapper/SDK model

- `DONE` prompt/skill resource diagnostics, collision handling, and reload visibility
  - duplicate prompt and skill names are detected while preserving first-match precedence
  - invalid or missing explicit resource paths now produce diagnostics
  - invalid skill metadata is surfaced as diagnostics instead of silently registering unusable commands
  - `get_commands` and `reload_resources` expose the current diagnostics to RPC clients

### RPC / remote control

- `DONE` JSON-RPC-like line-oriented command server
  - `pigo`: `pkg/codingagent/rpc.go`

- `DONE` commands for:
  - prompt
  - steer
  - follow_up
  - abort
  - new_session
  - branch
  - tree
  - set/get label
  - append/get custom entries
  - get_state
  - set_model
  - cycle_model
  - get_available_models
  - thinking-level and compaction/session operations

- `PARTIAL` exact parity with TypeScript RPC mode
  - the headless Go RPC surface is solid, but the full TS RPC/client semantics are broader

### Headless-relevant gaps still open

- `PARTIAL` extension runtime parity
  - TypeScript has `extensions/loader.ts`, `extensions/runner.ts`, `extensions/wrapper.ts`, SDK-facing bindings, and a larger event model
  - `pigo` does not yet provide an equivalent extension system

- `PARTIAL` prompt/resource/skill behavior depth
  - base support plus diagnostics/collision/reload visibility exist
  - richer resource semantics from TS are still broader, especially where they interact with extension wrappers and SDK behavior

- `PARTIAL` compaction/runtime workflow richness
  - session events and advanced branch-summary behaviors are still narrower

- `PARTIAL` coding-agent conformance breadth
  - the Go headless target is testable
  - the upstream TS package still has a much larger suite around session manager, compaction, extensions, interactive mode, and regressions

## Explicitly Out of Scope for the Current Go Target

These are real `pi-mono` features, but they should not be treated as blocking parity gaps if the target remains a headless Go port.

- `OOS` interactive TUI components
  - `packages/coding-agent/src/modes/interactive/*`

- `OOS` themes, keybinding UI, selector components, login dialogs, visual renderers

- `OOS` extension examples and interactive-only demo surfaces

- `OOS` full SDK parity for TypeScript consumers

- `OOS` byte-for-byte API surface identity where Go should use native types instead of reproducing TS ergonomics

## Recommended Next Work

If the goal is to keep closing practical parity gaps in order of value:

1. `ai`: remaining non-OpenAI provider fidelity gaps
2. `codingagent`: extension/runtime/resource parity for the headless target
3. `codingagent`: deeper session-manager and compaction semantics
4. `agentcore`: only then revisit exact proxy/event/API shape parity if a consumer truly needs it

## Implementation Todo

- `DONE` `codingagent`: prompt/skill resource diagnostics, collision handling, validation, and RPC reload visibility
- `TODO` `codingagent`: project context file loading parity for `AGENTS.md` / `CLAUDE.md` style context in headless prompts
- `TODO` `codingagent`: extension runtime, loader, wrapper, and SDK-facing command/tool parity for headless-safe extension flows
- `TODO` `codingagent`: richer session-manager and compaction workflow semantics, including branch-summary edge cases, abort/cancel events, context estimation, and extension hooks
- `TODO` `ai`: non-OpenAI provider long-tail fidelity against upstream provider-specific tests and quirks
- `TODO` `agentcore` / `ai`: exact normalized event payload and stream shape gaps where downstream clients depend on TS-compatible objects

## Bottom Line

`pigo` is no longer an early scaffold.

- `pkg/ai` is the strongest area and already comparable for a large portion of real usage.
- `pkg/agentcore` is functional and useful, but it is a smaller abstraction layer than the total TypeScript runtime surface.
- `pkg/codingagent` is usable for the headless target, but it still trails the richer TypeScript session/runtime/extension ecosystem.

That is the current codebase-based parity position after rescanning both repositories.
