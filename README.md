# pigo

Go implementation workspace for Pi.

This repository is intentionally separate from `pi-mono`. It starts with the language-neutral conformance contracts used to compare behavior against the TypeScript implementation.

## Scope

Initial targets:

- `pkg/ai`: provider-independent message, stream, tool-call, and usage contracts.
- `pkg/agentcore`: agent loop behavior over scripted assistant streams and tools.
- `pkg/codingagent`: headless coding-agent session behavior over a temporary workspace.

Out of scope for the first pass:

- TUI implementation.
- Full streaming parity across all providers.
- Full CLI parity.

## Conformance

Fixtures are copied from `pi-mono/packages/ai-conformance/fixtures`.

Run the Go conformance commands:

```bash
go run ./cmd/pigo-ai-conformance --case testdata/conformance/basic-text.json
go run ./cmd/pigo-ai-conformance --case testdata/conformance/thinking.json
go run ./cmd/pigo-ai-conformance --case testdata/conformance/image-content.json
go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-basic-tool.json
go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-multi-tool.json
go run ./cmd/pigo-agent-conformance --case testdata/conformance/agent-missing-tool.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-write-read.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit-ambiguous.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-bash.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-file-discovery.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-read-error.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-edit-error.json
go run ./cmd/pigo-coding-agent-conformance --case testdata/conformance/coding-agent-headless-bash-error.json
```

Run the minimal headless RPC server:

```bash
printf '{"id":"s1","type":"get_state"}\n' | go run ./cmd/pigo-rpc
printf '{"id":"stats1","type":"get_session_stats"}\n' | go run ./cmd/pigo-rpc --session-file tmp/session.jsonl
```

Current status: first conformance-backed tranche. The commands execute fixture-driven behavior for the current `pi-ai`, `agent-core`, and headless coding-agent fixtures, and their output passes the TypeScript verifiers.

`pkg/ai` also includes a complete provider catalog for all `pi-mono` `KnownProvider` values, with implemented OpenAI-compatible and
Anthropic-compatible transports plus explicit runtime placeholders for the rest.

OpenAI transport is currently wired for:

- `openai`
- `azure-openai-responses`
- `github-copilot`
- `deepseek`
- `xai`
- `groq`
- `cerebras`
- `openrouter`
- `huggingface`
- `zai`
- `opencode-go`

Anthropic transport is currently wired for:

- `anthropic`
- `fireworks`
- `minimax`
- `minimax-cn`
- `opencode`
- `vercel-ai-gateway`
- `kimi-coding`

Mistral transport is currently wired for:

- `mistral`

Google transport is currently wired for:

- `google`
- `google-vertex` via API-key path
- `google-gemini-cli`
- `google-antigravity`

Codex transport is currently wired for:

- `openai-codex`

Bedrock transport is currently wired for:

- `amazon-bedrock`

Every `KnownProvider` from `pi-mono` now resolves to a concrete transport in `pigo`, but several of those transports are still pragmatic request/response implementations rather than full streaming-equivalent ports.

Run the full cross-repo verifier:

```bash
make verify-conformance
```

This still is not a full production port. Provider coverage is now complete across the current `pi-mono` catalog, but broader CLI/runtime parity and deeper provider-specific streaming behavior remain follow-up implementation work.
