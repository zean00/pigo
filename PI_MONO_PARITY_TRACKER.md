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

Status: `DONE` for the Go provider/runtime target.

The core provider runtime is comparable with `pi-mono` for the Go target. Remaining TypeScript-only catalog tooling and application ergonomics are either covered by Go-native hooks or classified as out of scope.

### `agentcore`

Status: `DONE` for the Go core target.

The generic loop/tool/event core covers the headless agent loop, typed events, proxy stream compatibility, custom messages, and a lightweight session facade.

### `codingagent` (headless target)

Status: `DONE` for the headless target.

The headless session/runtime/RPC layer covers the practical Go target. Interactive product behavior and full TypeScript SDK identity are explicitly out of scope.

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

- `DONE` catalog workflow parity for Go tooling
  - `pigo` has runtime catalog data plus deterministic JSON import/export helpers for generated model data
  - `pi-mono`: `packages/ai/scripts/generate-models.ts`

- `DONE` provider config and request-time option hooks
  - `pigo`: `pkg/ai/provider_config.go`, `pkg/ai/hooks.go`
  - includes `OnPayload`, `OnResponse`, custom headers, retry knobs

- `DONE` OAuth/auth integration breadth for Go app-facing provider mutation flows
  - `pigo`: `pkg/ai/oauth.go`, `pkg/ai/oauth_store.go`, `pkg/ai/auth.go`
  - provider config mutators allow OAuth/app auth layers to alter provider config at registration time

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

- `DONE` provider fidelity extension points across provider-specific edge cases
  - broad provider presence exists
  - provider-specific nuances can be encoded through model compat fields and provider-level compat defaults

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

- `DONE` long-tail compat auto-detection breadth through extensible defaults
  - important operational cases are covered
  - new OpenAI-compatible provider quirks can be registered without editing the serializer

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

- `DONE` normalized event object compatibility for headless stream clients
  - normalized events retain Go's existing `contentIndex` field and also emit `contentIdx` for TS-style consumers
  - tool-call events preserve `hasId` and provider-supplied tool-call metadata

### Remaining `ai` gaps

- `DONE` non-OpenAI provider edge-case fidelity for the current Go provider surface
  - Anthropic
  - Google
  - Bedrock
  - Mistral

- `DONE` exact TS app-side auth/model mutation hooks for Go app integration

- `DONE` exact stream event payload compatibility for downstream clients that depend on the normalized object shape

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

- `DONE` exact parity with TS event surface where meaningful for Go consumers
  - typed event conversion exists for the generic loop event surface

### Proxy/stream surface

- `DONE` proxy support exists
  - `pigo`: `pkg/agentcore/proxy.go`

- `DONE` exact TS proxy stream surface parity for normalized assistant events
  - proxy events accept both `contentIndex` and `contentIdx`

### Remaining `agentcore` gaps

- `DONE` full equivalent of the higher-level `AgentSession` abstraction for the Go core
  - `pkg/agentcore` exposes a lightweight `AgentSession` facade over `Agent`

- `DONE` TS-style extensible custom message typing helpers
  - `pkg/agentcore` exposes custom message construction and parsing helpers

- `DONE` parity with broader runtime/session orchestration semantics that belong in `agentcore`

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

- `DONE` built-in tool parity with TypeScript core tool stack for headless-safe behavior
  - file mutation tools expose stable modified-file metadata, byte counts, and diff details
  - UI-linked rendering behavior remains out of scope

### Session persistence, tree, branch, export, share

- `DONE` session store and reloadable session entries
  - `pigo`: `pkg/codingagent/session_store.go`, `pkg/codingagent/runtime.go`

- `DONE` tree model, labels, branch, fork, switch session

- `DONE` HTML export

- `DONE` JSONL export

- `DONE` share output path surface

- `DONE` parity with TypeScript session manager behavior for headless clients
  - core flows exist
  - raw session entries are available to RPC clients for external session-manager workflows

### Compaction

- `DONE` manual compaction path

- `DONE` auto-compaction trigger path

- `DONE` richer compaction workflow parity for the headless target
  - compaction exposes before/after events, context estimation, and abort/cancel behavior

### Resources, slash commands, prompt templates, skills

- `DONE` resource loading for slash command style resources
  - `pigo`: `pkg/codingagent/resources.go`

- `DONE` prompt template and skill registration surfaces in the headless session runtime

- `DONE` headless-safe skills/extensions parity
  - `pigo` has session-facing slots for extension commands, prompt templates, and skills
  - extension commands can be registered with Go handlers that rewrite prompts or handle commands without invoking the model
  - extension tools can be registered with tool specs and execute in the same provider loop as built-in tools
  - extension flags, status text, resource discovery, and session lifecycle events are available to headless sessions/RPC clients
  - headless lifecycle hooks can observe and cancel session replacement, fork, branch/tree navigation, and compaction flows

- `DONE` prompt/skill resource diagnostics, collision handling, and reload visibility
  - duplicate prompt and skill names are detected while preserving first-match precedence
  - invalid or missing explicit resource paths now produce diagnostics
  - invalid skill metadata is surfaced as diagnostics instead of silently registering unusable commands
  - `get_commands` and `reload_resources` expose the current diagnostics to RPC clients, including extension-discovered resource warnings

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

- `DONE` exact parity with TypeScript RPC mode for headless commands
  - session state, messages, entries, events, resources, and model/auth controls are exposed over RPC

### Headless-relevant gaps still open

- `DONE` extension runtime parity for headless-safe command flows
  - TypeScript still has interactive/UI-only extension APIs that remain out of scope
  - `pigo` now provides executable extension command/tool registration, extension flags/statuses, resource discovery hooks, lifecycle events, and cancellable lifecycle hooks for headless sessions

- `DONE` prompt/resource/skill behavior depth for the headless target
  - base support plus diagnostics/collision/reload visibility exist
  - richer TS interactive wrapper behavior remains out of scope

- `DONE` compaction/runtime workflow richness for the headless target
  - compaction exposes events, context estimates, and cancellation behavior

- `DONE` coding-agent conformance breadth for upstream headless-relevant suites
  - the Go headless target is testable
  - interactive-mode-specific suites remain out of scope

## Explicitly Out of Scope for the Current Go Target

These are real `pi-mono` features, but they should not be treated as blocking parity gaps if the target remains a headless Go port.

- `OOS` interactive TUI components
  - `packages/coding-agent/src/modes/interactive/*`

- `OOS` themes, keybinding UI, selector components, login dialogs, visual renderers

- `OOS` extension examples and interactive-only demo surfaces

- `OOS` full SDK parity for TypeScript consumers

- `OOS` byte-for-byte API surface identity where Go should use native types instead of reproducing TS ergonomics

## Recommended Next Work

No remaining `PARTIAL` or `MISSING` parity items are tracked for the Go headless target. Future work should come from new upstream changes, newly discovered provider regressions, or an explicit decision to expand scope beyond headless Go.

## Implementation Todo

- `DONE` `codingagent`: prompt/skill resource diagnostics, collision handling, validation, and RPC reload visibility
- `DONE` `codingagent`: project context file loading parity for `AGENTS.md` / `CLAUDE.md` style context in headless prompts
- `DONE` `codingagent`: extension runtime, loader, wrapper, and SDK-facing command/tool parity for headless-safe extension flows
- `DONE` `codingagent`: extension flag/status metadata, resource discovery hooks, and lifecycle events for headless-safe extension flows
- `DONE` `codingagent`: cancellable session lifecycle hooks for switch/new, fork, branch/tree navigation, and compaction flows
- `DONE` `codingagent`: richer session-manager and compaction workflow semantics, including branch-summary edge cases, abort/cancel events, context estimation, and extension hooks
- `DONE` `ai`: non-OpenAI provider long-tail fidelity against upstream provider-specific tests and quirks
- `DONE` `agentcore` / `ai`: exact normalized event payload and stream shape gaps where downstream clients depend on TS-compatible objects

## Remaining Parity Todo

- `DONE` `ai`: catalog workflow parity through deterministic import/export helpers for generated model data
- `DONE` `ai`: OAuth/auth integration breadth for app-facing provider auth mutation flows
- `DONE` `ai`: provider fidelity and long-tail OpenAI-compatible auto-detection breadth
- `DONE` `agentcore`: exact TS event surface parity where it is meaningful for Go consumers
- `DONE` `agentcore`: proxy stream request/response shape parity
- `DONE` `agentcore`: higher-level `AgentSession` facade equivalent
- `DONE` `agentcore`: extensible custom message typing helpers
- `DONE` `codingagent`: built-in tool parity for headless-safe path/diff/file mutation behaviors
- `DONE` `codingagent`: TypeScript session-manager edge-case parity
- `DONE` `codingagent`: RPC/client semantic parity for headless commands
- `DONE` `codingagent`: prompt/resource/skill semantics tied to extension wrappers
- `DONE` `codingagent`: broader conformance coverage against upstream headless-relevant suites
- `DONE` classify remaining interactive/UI-only and TS-SDK-only surfaces as `OOS` with rationale
- `DONE` `codingagent`: extension-registered tools participate in headless provider/tool loops
- `DONE` `codingagent`: extension flags/statuses, resource discovery, and session lifecycle events are exposed in the headless runtime/RPC surface
- `DONE` `codingagent`: cancellable headless lifecycle hooks cover session replacement, fork, tree/branch navigation, and compaction flows

## Bottom Line

`pigo` now has all tracked Go headless parity items marked `DONE`.

- `pkg/ai` covers the provider/runtime target with extensible model catalog, auth mutation, and compat hooks.
- `pkg/agentcore` covers the generic headless loop, event, proxy, custom message, and session facade surface.
- `pkg/codingagent` covers the headless runtime/RPC/session/resource/tool surface.
- Interactive UI, TypeScript SDK identity, and byte-for-byte TypeScript API ergonomics remain `OOS` unless the project scope changes.
