# Nexus

A locally-run AI agent harness for developers. Nexus sits between you and your models — routing requests to the right tier automatically, enforcing governance, persisting memory across sessions, and exposing an OpenAI-compatible API so existing tools work without modification.

## Table of contents

- [Features](#features)
- [Quick start](#quick-start)
- [Installation](#installation)
- [Configuration](#configuration)
  - [Layers](#layers)
  - [Orchestrator](#orchestrator)
  - [Governance](#governance)
  - [Memory](#memory)
  - [Auth and rate limiting](#auth-and-rate-limiting)
  - [Logging and metrics](#logging-and-metrics)
- [CLI reference](#cli-reference)
- [REST API](#rest-api)
- [Architecture](#architecture)
- [Development](#development)
- [Build status](#build-status)
- [Docs](#docs)

---

## Features

- **3-layer hierarchical routing** — keyword classifier dispatches prompts to fast local (Layer 1), quality local (Layer 2), or cloud (Layer 3) based on intent. All layers and routing rules are configurable via YAML.
- **Automatic fallback** — if a layer is unavailable or errors, Nexus follows the configured `fallback_to` chain and logs a structured warning. No silent failures.
- **Ollama pre-warm** — on startup, Nexus pings each local Ollama model to verify it is loaded. Unresponsive layers are marked down and fallback activates before any user request hits them.
- **Cloud provider support** — Layer 3 can be backed by Anthropic (Claude), Google (Gemini), or OpenAI (GPT-4o), or kept local with a 70B Ollama model. Configured entirely in YAML; no code change required.
- **Session memory** — every conversation is persisted to a per-session SQLite file at `~/.nexus/sessions/<id>.db`. Resume any session with `nexus chat --session <id>`.
- **Episodic memory** — every request lifecycle is recorded as a causal event log in `~/.nexus/episodic.db`. Each session maps to an episode; the orchestrator writes `orchestrator_route`, `llm_call`, and `llm_response` events with latency and token counts. Episodes survive crashes — restart with `--session <id>` and new events are appended to the existing open episode. A background worker marks episodes that exceeded `open_episode_timeout` as failed.
- **Governance middleware** _(Phase 3)_ — pre-LLM input guard (PII, prompt injection detection) and post-LLM output guard (content filtering, token cap). Fully rule-driven; rejection returns a structured error.
- **OpenAI-compatible REST API** _(Phase 4)_ — `POST /v1/chat/completions` implements the OpenAI request/response schema so Continue, Cursor, and litellm work as drop-in clients with no modification.
- **Prometheus metrics** _(Phase 5)_ — request counts, latency histograms, governance rejection counts, and layer availability gauges on `GET /metrics`.
- **Single binary** — no external runtime, no Docker, no Node.js. One `nexus` binary on macOS arm64.

---

## Quick start

```bash
# 1. Build
make build

# 2. Write a minimal config (see Configuration section for full schema)
mkdir -p ~/.nexus
cat > ~/.nexus/config.yaml <<'EOF'
server:
  port: 11434

auth:
  api_keys:
    - key: "nexus-key-abc123"
      name: "default"
      rate_limit_rpm: 60

layers:
  layer1:
    provider: ollama
    base_url: http://localhost:11434
    model: llama3.2:3b
    timeout: 500ms
    fallback_to: layer2
  layer2:
    provider: ollama
    base_url: http://localhost:11434
    model: llama3.1:8b
    timeout: 5s
    fallback_to: layer3
  layer3:
    provider: ollama
    base_url: http://localhost:11434
    model: llama3.3:70b
    timeout: 60s

orchestrator:
  strategy: keyword
  rules:
    - pattern: "(what is|define|explain briefly)"
      layer: layer1
    - pattern: "(write|implement|refactor|debug)"
      layer: layer2
    - pattern: "(architect|design|analyse entire)"
      layer: layer3
  default_layer: layer2

logging:
  level: info
  format: json
  output: stdout

metrics:
  enabled: true
  path: /metrics
EOF

# 3. Validate the config
./bin/nexus config validate   # prints "OK"

# 4. Chat
./bin/nexus chat --prompt "what is 2+2"
```

---

## Installation

### Prerequisites

- [Go 1.24+](https://go.dev/dl/)
- [Ollama](https://ollama.com) running locally with at least one model pulled
- [golangci-lint](https://golangci-lint.run/usage/install/) (for `make lint` only)

### Build from source

```bash
git clone https://github.com/vishnuvardhansankoti/ai-nexus.git
cd ai-nexus
make build
# binary is at bin/nexus
```

### Add to PATH (optional)

```bash
cp bin/nexus /usr/local/bin/nexus
```

---

## Configuration

All configuration lives in `~/.nexus/config.yaml`. Every setting has a sensible default where possible. Changes take effect on restart — no hot-reload in v1.

Run `nexus config validate` to check your config for errors before starting.

### Layers

Nexus routes every prompt through one of three layers. Each layer points to an LLM backend.

```yaml
layers:
  layer1:
    provider: ollama          # ollama | anthropic | google | openai
    base_url: http://localhost:11434   # required for ollama
    model: llama3.2:3b
    timeout: 500ms
    fallback_to: layer2       # layer to try if this one fails; omit for no fallback

  layer2:
    provider: ollama
    base_url: http://localhost:11434
    model: llama3.1:8b
    timeout: 5s
    fallback_to: layer3

  layer3:
    # Option A — large local model (requires capable hardware)
    provider: ollama
    base_url: http://localhost:11434
    model: llama3.3:70b
    timeout: 60s

    # Option B — cloud provider
    # provider: anthropic
    # model: claude-sonnet-4-6
    # api_key: ""              # or set NEXUS_LAYER3_API_KEY env var
    # timeout: 30s
```

**Supported providers for Layer 3:**

| Provider | `provider:` value | Model examples |
|---|---|---|
| Ollama (local) | `ollama` | `llama3.3:70b`, `mistral:7b` |
| Anthropic | `anthropic` | `claude-sonnet-4-6`, `claude-haiku-4-5` |
| Google Gemini | `google` | `gemini-2.0-flash`, `gemini-1.5-pro` |
| OpenAI | `openai` | `gpt-4o`, `gpt-4o-mini` |

Cloud API keys can be set in the YAML `api_key` field or via the `NEXUS_LAYER3_API_KEY` environment variable (env var takes precedence when `api_key` is empty).

### Orchestrator

The orchestrator classifies each incoming prompt and maps it to a layer. In v1 the strategy is `keyword` — regex rules matched in order.

```yaml
orchestrator:
  strategy: keyword         # only option in v1
  rules:
    - pattern: "(what is|define|explain briefly)"
      layer: layer1         # fast, simple questions → small local model
    - pattern: "(write|implement|refactor|debug)"
      layer: layer2         # code tasks → larger local model
    - pattern: "(architect|design|analyse entire|compare at length)"
      layer: layer3         # deep reasoning → cloud or 70B
  default_layer: layer2     # no rule matches → use this layer
```

Rules are evaluated top-to-bottom; the first match wins. Patterns are compiled as Go regular expressions.

### Governance

Governance middleware sits between the orchestrator and the provider. Input rules run before the prompt reaches the LLM; output rules run before the response reaches the caller.

```yaml
governance:
  input:
    enabled: true
    rules:
      - name: pii_email
        pattern: '\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b'
        action: block         # block | redact | warn
      - name: prompt_injection
        pattern: "ignore previous instructions"
        action: block
  output:
    enabled: true
    max_tokens: 4096          # truncate responses longer than this
    disallowed_patterns: []
  cost:
    max_tokens_per_request: 8192
    warn_session_tokens: 100000   # log warning when session exceeds this
    block_session_tokens: 0       # 0 = no hard block
```

When a prompt is blocked, Nexus returns a structured error without calling the LLM:

```json
{"error": {"code": "governance_blocked", "rule": "pii_email", "message": "..."}}
```

### Memory

```yaml
memory:
  working:
    ttl: 30m                          # in-process buffer lifetime
  episodic:
    db: ~/.nexus/episodes.db          # SQLite path
    open_episode_timeout: 72h
    extraction_strategy: keyword      # keyword | llm (v2)
    vector_index: sqlite-vec
  semantic:
    enabled: false                    # v2
  archival:
    enabled: false                    # v2
```

Session files are written to `~/.nexus/sessions/<id>.db`. Each file holds the full message history for that session. Use `nexus chat --session <id>` to resume.

The episodic store (`memory.episodic.db`) records every request as a structured event log. Each session has one episode. Events written per turn:

| Event type | Written by | Contains |
|---|---|---|
| `episode_open` | `nexus chat` startup | session ID |
| `orchestrator_route` | Orchestrator | layer name, prompt length |
| `llm_call` | Orchestrator | layer, model |
| `llm_response` | Orchestrator | latency ms, tokens in/out, content snippet |
| `episode_close` | `nexus chat` exit | outcome, keyword summary |
| `governance_reject` / `governance_pass` | Governance _(Phase 3)_ | rule name |

Episodes have four outcomes: `success`, `failure`, `partial` (Ctrl+C), `escalated`. A background goroutine marks open episodes older than `open_episode_timeout` as `failed`.

### Auth and rate limiting

```yaml
auth:
  api_keys:
    - key: "nexus-key-abc123"
      name: "default"
      rate_limit_rpm: 60    # requests per minute; 0 = unlimited
    - key: "nexus-key-ci"
      name: "ci"
      rate_limit_rpm: 0
```

The REST API requires `Authorization: Bearer <key>` on every request. The CLI bypasses auth.

### Logging and metrics

```yaml
logging:
  level: info               # debug | info | warn | error
  format: json              # json | text
  output: stdout            # stdout | /path/to/file

metrics:
  enabled: true
  path: /metrics            # Prometheus scrape path
```

Every request log line includes: `trace_id`, `session_id`, `layer`, `model`, `latency_ms`, `tokens_in`, `tokens_out`, `governance_decision`.

---

## CLI reference

```
nexus [command] [flags]
```

### `nexus chat`

Start a conversation. Prompts are routed through the orchestrator automatically.

```bash
nexus chat                          # interactive REPL
nexus chat --prompt "explain defer" # single-shot; print response and exit
nexus chat --session <id>           # resume a previous session
```

On first run a session UUID is printed (`nexus session <uuid>`). Pass it to `--session` in future runs to resume with full context.

**Flags:**

| Flag | Short | Description |
|---|---|---|
| `--prompt` | `-p` | Send a single prompt and exit |
| `--session` | `-s` | Resume an existing session by ID |

### `nexus status`

Print the health of each configured layer. Pings Ollama layers; cloud layers are checked via a lightweight model-list call.

```bash
nexus status
# LAYER    PROVIDER    MODEL              STATUS    LATENCY
# layer1   ollama      llama3.2:3b        up        42ms
# layer2   ollama      llama3.1:8b        down      -
# layer3   anthropic   claude-sonnet-4-6  up        -
```

### `nexus config validate`

Load and validate `~/.nexus/config.yaml`. Prints `OK` on success or the validation error on failure.

```bash
nexus config validate
```

### `nexus config show`

Print the fully resolved configuration as indented JSON.

```bash
nexus config show
```

### `nexus version`

Print the version and build commit hash.

```bash
nexus version
# nexus v0.0.0-dev (ec56ddf)
```

### `nexus serve` _(Phase 4)_

Start the OpenAI-compatible REST API server.

```bash
nexus serve
```

---

## REST API

> **Status:** Implemented in Phase 4. The `nexus serve` command is a stub in the current build.

Once the server is running, point any OpenAI-compatible client at `http://localhost:<port>`:

```bash
curl http://localhost:11434/v1/chat/completions \
  -H "Authorization: Bearer nexus-key-abc123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "what is Go?"}]
  }'
```

### Endpoints

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat; set `"model": "auto"` to use the orchestrator |
| `GET` | `/v1/models` | List configured layers as model entries |
| `GET` | `/healthz` | Health check; returns `{"status":"ok"}` or `{"status":"degraded"}` |
| `GET` | `/metrics` | Prometheus text format |

The full API surface is documented in [`openapi.yaml`](openapi.yaml).

### IDE integration

To use Nexus as a backend for [Continue](https://continue.dev) or [Cursor](https://cursor.sh):

1. Start `nexus serve`
2. Set the API base URL to `http://localhost:11434`
3. Set the API key to any key configured in `auth.api_keys`
4. Select model `nexus-layer1`, `nexus-layer2`, or `nexus-layer3`; or use `auto` to let the orchestrator decide

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    nexus binary                     │
│                                                     │
│  CLI (cobra)          REST API (chi, Phase 4)       │
│       │                      │                      │
│       └──────────┬───────────┘                      │
│                  ▼                                  │
│           Orchestrator                              │
│      (keyword classifier)                           │
│                  │                                  │
│          Governance (Phase 3)                       │
│       input guard │ output guard                    │
│                  │                                  │
│    ┌─────────────┼─────────────┐                   │
│    ▼             ▼             ▼                   │
│  Layer 1       Layer 2       Layer 3               │
│  (Ollama)     (Ollama)   (Ollama/Cloud)            │
│                                                     │
│  Working Memory ──► Episodic Store (SQLite)         │
└─────────────────────────────────────────────────────┘
```

### Package layout

```
cmd/nexus/          CLI entry point and subcommands
api/                REST API server (Phase 4)
orchestrator/       Keyword classifier + layer dispatch + fallback chain
governance/         Input/output guard middleware (Phase 3)
memory/
  working/          In-process buffer + per-session SQLite file
  episodic/         Causal event log — episodes + events tables, FTS5 index, timeout worker
  semantic/         Document graph (Phase 6, stub)
  archival/         RAG vector store (Phase 6, stub)
providers/
  ollama/           Ollama HTTP client
  cloud/
    anthropic/      Anthropic Claude adapter
    google/         Google Gemini adapter
    openai/         OpenAI / compatible-endpoint adapter
  factory.go        Constructs the correct provider from LayerConfig
config/             YAML loader + struct validation
sdk/                Versioned interfaces (Provider, Agent, Tool, Skill, MemoryStore)
registry/           Tools + skills registries (Phase 7, stub)
agents/coding/      Coding sub-agent (Phase 8, stub)
```

### Import rules

Packages may only import in the following directions (no circular imports):

```
cmd           →  everything
api           →  orchestrator, governance, memory/*, providers/*, config, sdk
orchestrator  →  providers/*, config, sdk, memory/episodic
governance    →  config, sdk
memory/*      →  config, sdk
providers/*   →  sdk
sdk           →  nothing
```

---

## Development

```bash
make build    # compile bin/nexus
make test     # go test ./...
make lint     # golangci-lint run ./...
make clean    # remove bin/
```

### Running tests

Tests are fully mock-based — no live Ollama instance or network access required.

```bash
go test ./...             # all packages
go test ./orchestrator/...  # specific package
go test -v ./providers/ollama/...  # verbose
```

### Adding a new provider

1. Create `providers/cloud/<name>/<name>.go` implementing `sdk.Provider`
2. Add a case to `providers/factory.go`
3. Add `"<name>"` to the `oneof` validator in `config/config.go` (`LayerConfig.Provider`)
4. Write unit tests using `net/http/httptest` or a mock struct

### Adding a new CLI command

1. Create `cmd/nexus/<command>.go` in `package main`
2. Define a `*cobra.Command` var
3. Register it in `rootCmd.AddCommand(...)` in `cmd/nexus/main.go`

---

## Build status

| Phase | Scope | Status |
|---|---|---|
| 0 — Scaffold | Repo structure, SDK interfaces, config loader, CI | ✅ Done |
| 1 — Routing + CLI | Ollama + cloud providers, orchestrator, `nexus chat` | ✅ Done |
| 2 — Episodic Store | SQLite causal event log, crash recovery | ✅ Done |
| 3 — Governance | Input/output guard middleware | Planned |
| 4 — REST API | OpenAI-compatible HTTP server | Planned |
| 5 — Observability | Structured logs, Prometheus metrics | Planned |

v2 adds: cloud providers for all layers, semantic memory, tools/skills registry, coding sub-agent (PM → Analyst → Architect → Developer → Tester), and a SvelteKit dashboard embedded in the binary.

---

## Docs

- [`docs/idea.md`](docs/idea.md) — full feature specification
- [`docs/prd.md`](docs/prd.md) — product requirements and v1 config schema
- [`docs/dev-plan.md`](docs/dev-plan.md) — phase-by-phase implementation plan
- [`docs/phase0-agent-brief.md`](docs/phase0-agent-brief.md) — agent brief: repository scaffold
- [`docs/phase1-agent-brief.md`](docs/phase1-agent-brief.md) — agent brief: 3-layer routing + CLI
- [`docs/phase2-agent-brief.md`](docs/phase2-agent-brief.md) — agent brief: episodic store
- [`openapi.yaml`](openapi.yaml) — OpenAPI 3.1 specification for the REST API
