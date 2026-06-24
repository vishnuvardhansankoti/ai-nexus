# Nexus

A locally-run AI agent harness for developers. Nexus sits between you and your models — routing requests to the right tier automatically, enforcing governance, persisting memory across sessions, and exposing an OpenAI-compatible API so existing tools work without modification.

## Features

- **3-layer hierarchical routing** — keyword classifier dispatches prompts to fast local (Layer 1), quality local (Layer 2), or cloud (Layer 3) based on intent. All layers are configurable via YAML.
- **Governance middleware** — pre-LLM input guard (PII, prompt injection) and post-LLM output guard (content, token cap). Fully rule-driven.
- **Episodic memory** — causal event log in SQLite survives crashes and spans sessions. Resume any session with `nexus chat --session <id>`.
- **OpenAI-compatible REST API** — `POST /v1/chat/completions` works with Continue, Cursor, and litellm without client-side changes.
- **Single binary** — no external runtime, no Docker, no Node. One `nexus` binary on macOS arm64.

## Quick start

```bash
# Build
make build

# Validate your config
./bin/nexus config validate

# Start a chat session
./bin/nexus chat

# Single-shot prompt
./bin/nexus chat --prompt "what is 2+2"

# Resume a session
./bin/nexus chat --session <id>

# Check layer health
./bin/nexus status

# Start the REST API server
./bin/nexus serve
```

## Configuration

Nexus is configured via `~/.nexus/config.yaml`. All routing rules, model endpoints, governance policies, and memory settings live here. Changes take effect on restart.

```yaml
layers:
  layer1:
    provider: ollama
    model: llama3.2:3b
    timeout: 500ms
    fallback_to: layer2
  layer2:
    provider: ollama
    model: llama3.1:8b
    timeout: 5s
    fallback_to: layer3
  layer3:
    provider: anthropic        # or ollama | google | openai
    model: claude-sonnet-4-6
    api_key: ""                # or NEXUS_LAYER3_API_KEY env var
    timeout: 30s

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
```

See `docs/prd.md` section 8 for the full config schema.

## REST API

Start the server with `nexus serve`, then point any OpenAI-compatible client at it:

```bash
curl http://localhost:11434/v1/chat/completions \
  -H "Authorization: Bearer nexus-key-abc123" \
  -H "Content-Type: application/json" \
  -d '{"model": "auto", "messages": [{"role": "user", "content": "hello"}]}'
```

Endpoints: `POST /v1/chat/completions` · `GET /v1/models` · `GET /healthz` · `GET /metrics`

## Development

```bash
make build    # build bin/nexus
make test     # run tests
make lint     # golangci-lint
make clean    # remove bin/
```

Requires Go 1.24+ and [golangci-lint](https://golangci-lint.run/usage/install/).

## Status

| Phase | Scope | Status |
|---|---|---|
| 0 — Scaffold | Repo structure, interfaces, config loader, CI | ✅ Done |
| 1 — Routing + CLI | Ollama providers, orchestrator, `nexus chat` | 🔜 Next |
| 2 — Episodic Store | SQLite event log, crash recovery | Planned |
| 3 — Governance | Input/output guard middleware | Planned |
| 4 — REST API | OpenAI-compatible HTTP server | Planned |
| 5 — Observability | Structured logs, Prometheus metrics | Planned |

v2 adds cloud providers for all layers, semantic memory, tools/skills registry, coding sub-agent, and SvelteKit dashboard.

## Docs

- [`docs/idea.md`](docs/idea.md) — full feature specification
- [`docs/prd.md`](docs/prd.md) — product requirements and config schema
- [`docs/dev-plan.md`](docs/dev-plan.md) — phase-by-phase implementation plan
- [`openapi.yaml`](openapi.yaml) — API specification
