# Installation

## Prerequisites

- Go 1.25 or newer, matching `go.mod`.
- Git.
- A local checkout of `pi-mono` at `../pi-mono` if you want to run parity checks.
- Node.js and npm if you want to run the TypeScript-side `pi-mono` conformance verifiers.
- Python with MkDocs if you want to build this documentation.

## Clone and Test

```bash
git clone https://github.com/zean00/pigo.git
cd pigo
go test ./...
```

## Build Commands

Build the ACP adapter:

```bash
go build ./cmd/pigo-acp
```

Build all command packages:

```bash
go build ./cmd/...
```

## Run the ACP Adapter

`pigo-acp` speaks JSON-RPC over stdio:

```bash
go run ./cmd/pigo-acp
```

A minimal initialize request:

```bash
printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}\n' | go run ./cmd/pigo-acp
```

## Run the RPC Adapter

The JSONL RPC command is useful for direct headless runtime testing:

```bash
printf '{"id":"s1","type":"get_state"}\n' | go run ./cmd/pigo-rpc
```

Use a session file:

```bash
printf '{"id":"stats1","type":"get_session_stats"}\n' | go run ./cmd/pigo-rpc --session-file tmp/session.jsonl
```

Enable a per-session usage quota:

```bash
printf '{"id":"q1","type":"set_usage_quota","mode":"enforce","maxTotalTokens":100000}\n' | go run ./cmd/pigo-rpc --session-file tmp/session.jsonl
```

Use an OAuth credential store:

```bash
printf '{"id":"o1","type":"get_oauth_providers"}\n' | go run ./cmd/pigo-rpc --auth-file tmp/auth.json
```

## Build Documentation

Install MkDocs if it is not already available:

```bash
python -m pip install mkdocs
```

Build the static site:

```bash
mkdocs build
```

Serve the docs locally:

```bash
mkdocs serve
```
