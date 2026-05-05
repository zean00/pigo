# Configuration

`pigo` can be configured through command flags, provider environment variables, OAuth stores, model/provider registration, and MCP server configuration.

## Command Flags

### `pigo-acp`

```bash
go run ./cmd/pigo-acp --auth-file tmp/auth.json
```

- `--auth-file`: optional OAuth credential store used by ACP sessions.

### `pigo-rpc`

```bash
go run ./cmd/pigo-rpc --cwd . --session-file tmp/session.jsonl --auth-file tmp/auth.json
```

- `--cwd`: workspace root.
- `--session-file`: optional JSONL session file.
- `--auth-file`: optional OAuth credential store.

## Provider Environment Variables

Provider API keys are read from environment variables. Common examples:

| Provider | Environment variable |
| --- | --- |
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` or `ANTHROPIC_OAUTH_TOKEN` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Google Gemini | `GEMINI_API_KEY` |
| Google Vertex | `GOOGLE_CLOUD_API_KEY` |
| Mistral | `MISTRAL_API_KEY` |
| Groq | `GROQ_API_KEY` |
| Cerebras | `CEREBRAS_API_KEY` |
| xAI | `XAI_API_KEY` |
| Hugging Face router | `HF_TOKEN` |
| Amazon Bedrock | AWS standard credentials or `AWS_BEARER_TOKEN_BEDROCK` |

## OAuth

Use the OAuth helper to list supported OAuth providers:

```bash
go run ./cmd/pigo-auth --list
```

Start an OAuth flow:

```bash
go run ./cmd/pigo-auth --provider anthropic --auth-file tmp/auth.json
go run ./cmd/pigo-auth --provider openai-codex --auth-file tmp/auth.json
```

Google Gemini CLI and Antigravity OAuth require caller-provided desktop OAuth client credentials:

```bash
export PIGO_GEMINI_CLI_OAUTH_CLIENT_ID=...
export PIGO_GEMINI_CLI_OAUTH_CLIENT_SECRET=...
export PIGO_ANTIGRAVITY_OAUTH_CLIENT_ID=...
export PIGO_ANTIGRAVITY_OAUTH_CLIENT_SECRET=...
```

OAuth secrets are stored through the dedicated OAuth store, not normal session history.

## Local LLMs

Local LLM servers are supported through the OpenAI-compatible transport when they expose a `/v1/chat/completions` API. This covers common local runtimes such as Ollama, llama.cpp server, and LM Studio.

Typical base URLs:

| Runtime | Base URL |
| --- | --- |
| Ollama | `http://localhost:11434/v1` |
| llama.cpp server | `http://localhost:8080/v1` |
| LM Studio | `http://localhost:1234/v1` |

Register a custom provider/model with `API: "openai-completions"` and point `BaseURL` at the local server:

```go
err := ai.RegisterProviderConfig(ai.ProviderConfig{
	Name:    "ollama-local",
	API:     "openai-completions",
	BaseURL: "http://localhost:11434/v1",
	APIKey:  "local",
	Models: []ai.Model{{
		ID: "llama3.1",
	}},
})
```

Most local servers ignore the API key, but `RegisterProviderConfig` currently requires a non-empty value when defining models, so use a harmless placeholder such as `local`.

## Session Purpose And Context

`pigo` defaults to `coding` purpose because the project is focused on a headless coding agent. Embedders can use lighter domain framing without changing the provider loop or tool implementations:

| Purpose | Prompt framing |
| --- | --- |
| `coding` | Headless coding agent that can inspect and modify workspace files. |
| `generic` | Headless workspace agent for tasks that are not necessarily coding-specific. |
| `research` | Headless research agent that gathers evidence and cites sources when available. |
| `readonly` | Headless read-only agent that should inspect information without modifying files or running destructive commands. |

Purpose only changes system-prompt/context behavior. It does not enable or disable tools. Use built-in tool policy, bash permissions, research tool config, and MCP config to control what the model can actually call.

Environment defaults:

```bash
export PIGO_SESSION_PURPOSE=research
export PIGO_CONTEXT_FILES=AGENTS.md,RESEARCH.md
export PIGO_INCLUDE_GIT_CONTEXT=false
export PIGO_INCLUDE_PACKAGE_CONTEXT=false
```

Context file values must be file names only, not paths. Values such as `../SECRET.md` or `nested/AGENTS.md` are rejected; invalid environment defaults fall back to the safe built-in domain config.

ACP sessions expose these config options:

- `session_purpose`
- `context_files`
- `include_git_context`
- `include_package_context`
- `extra_instructions`

The JSONL RPC adapter supports:

- `set_domain_config`
- `get_domain_config`

Example RPC update:

```json
{"id":"d1","type":"set_domain_config","purpose":"readonly","contextFiles":["AGENTS.md"],"includeGitContext":true,"includePackageContext":false}
```

## Command Output Compression

`pigo` can compress noisy bash command output before it is stored in session history or sent back to the model. This is inspired by RTK-style command filtering, but implemented natively inside the headless coding-agent runtime.

Compression never changes the executed command, exit code, or cancellation behavior. It only changes the output text recorded for the agent and client, and it includes metadata such as the filter used, original byte count, compressed byte count, and truncation state.

Environment defaults:

```bash
export PIGO_COMMAND_COMPRESSION=auto
export PIGO_COMMAND_COMPRESSION_ENABLE=go-test,git-diff
export PIGO_COMMAND_COMPRESSION_DISABLE=generic
export PIGO_COMMAND_COMPRESSION_MAX_BYTES=20000
```

Modes:

| Mode | Behavior |
| --- | --- |
| `off` | Disable command-aware compression and keep only the existing hard output limit. |
| `auto` | Compress when output is large or when a command-specific filter is useful. |
| `force` | Run the best matching enabled filter even for smaller output. |

Built-in filters:

| Filter | Commands |
| --- | --- |
| `go-test` | `go test ...` |
| `git-diff` | `git diff ...` |
| `git-status` | `git status ...` |
| `grep` | `rg ...`, `grep ...` |
| `list` | `ls ...`, `find ...` |
| `generic` | fallback long-output truncation |

ACP sessions expose these config options:

- `command_compression`
- `command_compression_enabled_filters`
- `command_compression_disabled_filters`

The JSONL RPC adapter supports:

- `set_command_compression`
- `get_command_compression`

Validation commands:

```bash
go test ./pkg/codingagent -run TestRPCCommandCompressionConfig
OPENROUTER_API_KEY=... go test ./pkg/codingagent -run TestLiveOpenRouterCommandCompression -count=1
```

The live OpenRouter test is skipped when `OPENROUTER_API_KEY` is not set. It asks a real model to invoke the bash tool and verifies that the resulting tool output contains compression metadata.

## Tool Execution

`pigo` can control how model-requested tool calls are executed:

| Mode | Behavior |
| --- | --- |
| `parallel` | Execute one assistant turn's tool calls concurrently and return the full batch. This is the default. |
| `sequential` | Execute one assistant turn's tool calls in source order and return the full batch. |
| `interleaved` | Execute one tool call, return its result, then let the model reason again before the next tool. |

When `interleaved` is active, streaming assistant updates also expose only the first tool call from a provider turn. This keeps event consumers aligned with the final assistant message and the tool execution events that follow.

Set the default for new sessions with:

```bash
PIGO_TOOL_EXECUTION=interleaved
```

ACP clients can set `tool_execution` through `session/set_config_option`.

## Usage Ledger And Quotas

`pigo` records provider-reported usage on assistant messages and can persist per-call usage ledger entries in the session JSONL log. The ledger is per session and includes provider, model, token usage, estimated cost, and whether model pricing was known.

Usage quotas are optional and disabled by default. When enabled, pigo checks the quota before every provider call. If a response exceeds a quota, that response is recorded and the next provider call is blocked.

Environment defaults:

```bash
export PIGO_USAGE_QUOTA=off
export PIGO_USAGE_MAX_TOTAL_TOKENS=100000
export PIGO_USAGE_MAX_COST=1.50
```

Supported quota variables:

- `PIGO_USAGE_QUOTA`: `off` or `enforce`.
- `PIGO_USAGE_MAX_INPUT_TOKENS`
- `PIGO_USAGE_MAX_OUTPUT_TOKENS`
- `PIGO_USAGE_MAX_CACHE_READ_TOKENS`
- `PIGO_USAGE_MAX_CACHE_WRITE_TOKENS`
- `PIGO_USAGE_MAX_TOTAL_TOKENS`
- `PIGO_USAGE_MAX_COST`

JSONL RPC supports `set_usage_quota`, `get_usage_quota`, and `get_usage_ledger`. ACP clients can set the same quota fields with `session/set_config_option`.

Cost quotas use model catalog pricing when available. If pricing is unknown, token quotas still work and quota status reports a warning instead of blocking solely on cost.

## Bash Command Permissions

`pigo` can restrict which commands may run through the bash tool and direct RPC bash command. The default mode is `allow-all`, which preserves the current behavior unless a deny rule matches.

Environment defaults:

```bash
export PIGO_BASH_PERMISSION_MODE=allow-all
export PIGO_BASH_ALLOW='glob:go test*,exact:git status --short'
export PIGO_BASH_DENY='glob:rm *,regex:^sudo\\b'
```

Modes:

| Mode | Behavior |
| --- | --- |
| `allow-all` | Allow every command unless it matches a deny rule. |
| `allow-list` | Allow only commands that match an allow rule and do not match a deny rule. |

Rule syntax:

| Prefix | Behavior |
| --- | --- |
| `exact:` | Match the complete command string. |
| `glob:` | Match with shell-style glob patterns. |
| `regex:` | Match with a Go regular expression. |

Deny rules always win over allow rules. Denied commands are not executed; they return a normal error-style bash/tool result with permission metadata.

ACP sessions expose these config options:

- `bash_permission_mode`
- `bash_permission_allow`
- `bash_permission_deny`

The JSONL RPC adapter supports:

- `set_bash_permission_policy`
- `get_bash_permission_policy`

## Built-in Tool Policy

The model-facing built-in tools are:

- `bash`
- `write`
- `read`
- `edit`
- `ls`
- `grep`
- `find`

By default all built-in tools are exposed. You can limit that surface with an enabled list, a disabled list, or both. Disabled tools always win.

Environment defaults:

```bash
export PIGO_BUILTIN_TOOLS='read,grep,ls'
export PIGO_DISABLED_BUILTIN_TOOLS='bash'
```

Behavior:

| Setting | Behavior |
| --- | --- |
| No lists | Expose every built-in tool. |
| `PIGO_BUILTIN_TOOLS` | Expose only the listed built-in tools. |
| `PIGO_DISABLED_BUILTIN_TOOLS` | Remove the listed built-in tools from the exposed set. |

ACP sessions expose these config options:

- `builtin_tools_enabled`
- `builtin_tools_disabled`

The JSONL RPC adapter supports:

- `set_builtin_tool_policy`
- `get_builtin_tool_policy`

This policy controls built-in model tools. Extension tools and MCP tools are attached separately and are not filtered by this policy.

The built-in `grep` tool is also the local search primitive used for future research orchestration. `pigo` does not define a duplicate research-specific grep tool.

## Agent Profiles And Workflow Metadata

`pigo` can load lightweight agent resources from:

- `~/.pi/agent/agents`
- `<workspace>/.pi/agents`

Agent profiles are markdown files with optional frontmatter:

```markdown
---
name: reviewer
description: Review code changes
provider: openai
model: gpt-4.1
thinkingLevel: medium
tools: read,grep
---

Focus on correctness, regressions, and missing tests.
```

The profile body is appended to the headless system prompt when the profile is active. `provider`, `model`, and `thinkingLevel` are applied only when present. Set `agent_profile` to `default` to clear the active profile.

Teams and chains are loaded as metadata from `teams.yaml` and `chains.yaml` in the same directories:

```yaml
teams:
  - name: delivery
    description: Delivery team
    agents: [planner, reviewer]
```

```yaml
chains:
  - name: review-chain
    steps:
      - name: plan
        agent: planner
        prompt: Plan this change
      - name: review
        agent: reviewer
        prompt: Review this change
```

ACP sessions expose `agent_profile` through `session/set_config_option` and include loaded profiles, teams, and chains in session state. JSONL RPC supports `get_agent_profiles`, `set_agent_profile`, `get_agent_teams`, and `get_agent_chains`.

This is a configuration/resource layer, not full multi-agent orchestration. pigo does not execute teams or chains by itself.

## Tool Search

`tool_search` is an optional read-only discovery tool that returns visible tool names, descriptions, and source categories. It does not invoke tools.

Enable it with:

```bash
export PIGO_TOOL_SEARCH=1
```

ACP clients can also set `tool_search` to `on` or `off` with `session/set_config_option`.

## Research Tools

`pigo` can expose optional internet research tools. They are disabled by default and must be explicitly enabled:

```bash
export PIGO_RESEARCH_TOOLS='research,search,scrape,security_search'
export PIGO_SEARXNG_URL='http://localhost:8080'
export PIGO_OBSCURA_URL='http://localhost:9222'
export PIGO_NVD_API_KEY='optional-nvd-api-key'
```

Available research tools:

| Tool | Behavior |
| --- | --- |
| `research` | Run an isolated quick research sub-agent and return a Markdown report. |
| `search` | Query an external SearXNG instance and return titles, URLs, and snippets. |
| `scrape` | Fetch HTTP(S) URLs and extract compact readable text. It can use static HTTP or Obscura-rendered scraping. |
| `security_search` | Search public vulnerability sources such as OSV, NVD, and CISA KEV. |

The `research` tool accepts `query`, optional `depth` (currently only `0` quick mode), and optional `model`. A bare `model` value keeps the parent session provider; `provider/model` switches the quick research sub-agent to that provider and model.

`PIGO_SEARXNG_URL` falls back to `SEARXNG_URL`. Production sessions use an external SearXNG URL; local validation can start a disposable Docker SearXNG container with the smoke script below.

`PIGO_OBSCURA_URL` falls back to `OBSCURA_URL`. Point it at a Docker-hosted Obscura CDP server, for example `http://localhost:9222` from `obscura serve --port 9222`. The `scrape` tool uses it when called with `engine: "obscura"` or `render: true`.

A stealth-mode Obscura Compose template is available in `deploy/obscura`:

```bash
cd deploy/obscura
docker compose up -d --build
export PIGO_OBSCURA_URL=http://localhost:9222
```

The template uses host networking because the current Obscura `serve` command binds to `127.0.0.1` inside the container.

`PIGO_NVD_API_KEY` falls back to `NVD_API_KEY`. It is optional, but helps avoid anonymous NVD API rate limits. ACP config state reports only whether a key is configured, not the key value.

ACP sessions expose these config options:

- `research_tools`
- `research_searxng_url`
- `research_obscura_url`
- `research_nvd_api_key`

The JSONL RPC adapter supports:

- `set_research_tools`
- `get_research_tools`

Local SearXNG smoke validation can be run with Docker:

```bash
scripts/test-searxng.sh
```

The script starts `searxng/searxng`, waits for JSON search readiness, runs the live `search` smoke test, and removes the container.

## MCP Configuration

MCP config is loaded in this order:

1. ACP `mcpServers` session parameters.
2. `PI_MCP_CONFIG_JSON`.
3. `PI_MCP_CONFIG`.
4. `.pi/mcp.json`.
5. `~/.pi/agent/mcp.json`.

Example JSON file:

```json
{
  "servers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "remote": {
      "type": "http",
      "url": "http://localhost:3000/mcp"
    }
  }
}
```

Set a config path:

```bash
export PI_MCP_CONFIG=/path/to/mcp.json
```

Or inline JSON:

```bash
export PI_MCP_CONFIG_JSON='{"servers":{"demo":{"type":"http","url":"http://localhost:3000/mcp"}}}'
```
