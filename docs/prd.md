# Nexus — Product Requirements Document

## 1. Executive Summary

Nexus is a locally-run AI agent harness for developers who want intelligent, privacy-first access to language models. It sits between the developer and their models — routing requests to the right model tier automatically, enforcing governance, persisting memory across sessions, and exposing a standard API so existing tools work without modification. The v1 target is a solo developer on macOS who wants a single binary that replaces ad-hoc Ollama usage with a configurable, observable, and extensible AI layer.

---

## 2. Problem Statement

Existing developer AI tools have one of two failure modes:

| Tool | Problem |
|---|---|
| Raw Ollama | No routing intelligence. Developer manually picks model for every prompt. No memory, no governance, no API compatibility. |
| Copilot CLI / Claude CLI | Cloud-only. All prompts leave the machine. No cost control. No fallback. Cannot be self-hosted. |
| LiteLLM / local proxies | Routing is manual config, not intent-aware. No memory. No governance. Not a single binary. |

The specific gaps Nexus addresses:
- **No routing intelligence** — today you pick the model; Nexus picks the layer based on prompt complexity.
- **No privacy control** — cloud tools always send data to a provider; Nexus is fully offline in v1.
- **No cross-session memory** — every conversation starts cold; Nexus persists episodic context.
- **No governance** — no PII filtering, no cost caps, no injection detection at the tool layer.
- **No extensibility** — existing tools have no plugin interface for agents, skills, or tools.

---

## 3. User Personas

### P1 — Solo Developer (v1 primary)
- Runs Nexus locally on macOS. All models run via Ollama.
- Primary concern: privacy (prompts never leave the machine), cost control (no cloud spend), and productivity (fast responses for simple queries, quality responses for complex ones).
- Uses the CLI daily. May also point an IDE plugin (Continue, Cursor) at the Nexus REST endpoint.
- Does not need multi-user features, access control, or a web UI in v1.

### P2 — Small Team, 2–10 devs (v2)
- Shared Nexus instance on a team server. Multiple developers, each with their own API key.
- Needs per-user rate limiting, spend tracking, and basic access control.
- May mix local and cloud layers depending on task sensitivity.

### P3 — Platform / Enterprise Team (v3)
- Multi-tenant deployment. Audit logs, SSO, strict governance, cloud + local hybrid routing.
- Significantly larger scope; not in scope for this document.

**Design principle:** The v1 architecture must accommodate P2 and P3 by adding config and middleware — the core routing, memory, and governance packages must not require restructuring.

---

## 4. v1 Scope

### In scope

| Feature | Minimum v1 bar |
|---|---|
| **3-layer routing orchestrator** | Keyword classifier assigns intent category; deterministic YAML rule maps category to layer. Layer 1 and Layer 2 are always Ollama local models. Layer 3 provider is configurable: `ollama` (large local model if hardware supports it) or any supported cloud provider (`anthropic`, `google`, `openai`). |
| **Layer fallback** | If a layer is unavailable (Ollama timeout or cloud API error), fall back to the next layer and log a structured warning. |
| **Ollama pre-warm** | On startup, Nexus sends a warm-up request to each configured Ollama model. Configurable timeout; unavailable layer is marked down, fallback activates. Cloud-backed Layer 3 is validated by a lightweight ping (model list call) instead of a warm-up inference. |
| **CLI** | `nexus chat` (interactive REPL + single-shot `--prompt` flag), `nexus status` (layer health + model availability), `nexus config validate` (YAML validation). |
| **Session resume** | CLI session state written to `~/.nexus/sessions/<id>.db` (SQLite). `nexus chat --session <id>` resumes with full context. |
| **YAML config** | All layers, orchestrator rules, memory, and governance are configurable via a single `~/.nexus/config.yaml`. Changes take effect on restart; no code change required. |
| **Working Context (Tier 0)** | In-process message buffer for the active session. Flushed to session file on each turn for resume capability. TTL configurable. |
| **Episodic Store (Tier 1)** | SQLite at `~/.nexus/episodes.db`. Writes ordered, causal event log (routing decisions, LLM calls, governance outcomes) per episode. Episode survives crashes; coding sub-agent (v2) will use this for state recovery. Temporal + structural query interfaces. Keyword-based fact extractor runs on episode close (Tier 2 writes are a no-op stub in v1). |
| **Basic governance** | Pre-LLM: keyword/regex input guard (PII patterns, prompt injection signatures). Post-LLM: output length cap, disallowed-content keyword check. Per-request token budget cap. Per-session cost accumulator (logged; optionally blocks the request). All rules configurable in YAML. Rejection returns structured error. |
| **REST API** | HTTP server. `POST /v1/chat/completions` implements the OpenAI request/response schema so Continue, Cursor, and litellm work without modification. API key auth (`Authorization: Bearer <key>`). Per-key rate limiter. `GET /healthz`. `GET /metrics` (Prometheus). `GET /v1/models` (lists configured layers as model entries). |
| **OpenAPI spec** | `openapi.yaml` in the repo documents the full API surface. |
| **Structured logging** | JSON logs via `slog`. Every request carries: trace ID, session ID, layer selected, model used, latency ms, token counts, governance decision. |
| **Prometheus metrics** | `/metrics` endpoint. Counters: request count by layer, governance rejection count. Histograms: latency by layer. Gauges: layer availability. No external dependency required. |

### v1 CLI commands summary

```
nexus chat                         # interactive session
nexus chat --prompt "..."          # single-shot
nexus chat --session <id>          # resume session
nexus status                       # layer health + model status
nexus config validate              # validate config.yaml
nexus config show                  # print resolved config
nexus version                      # print version + build info
```

---

## 5. v1 Non-Goals

These are explicitly deferred to v2 or later. No stubs, no partial implementations.

| Feature | Deferred to |
|---|---|
| Cloud providers for Layer 1 or Layer 2 | v2 (Layer 3 cloud is v1; Layers 1 and 2 are Ollama-only in v1) |
| Dashboard / SvelteKit UI | v2 |
| Coding sub-agent (PM/Analyst/Architect/Developer/Tester) | v2 |
| Semantic Store (Tier 2 memory) | v2 |
| Archival / RAG (Tier 3 memory) | v2 |
| Tools and Skills registry | v2 |
| OpenTelemetry distributed tracing | v2 |
| Embedding-based or LLM-based orchestrator classifier | v2 (keyword only in v1) |
| JWT auth / multi-user access control | v2 |
| llama-guard or model-based governance | v2 (keyword/regex only in v1) |
| Windows support | v2 (Linux amd64 is CI-tested in v1 but not a release target) |
| Config hot-reload (no restart) | v2 |
| Web-based config editor | never (config is always file-based) |

---

## 6. Success Metrics for v1

Nexus v1 is done when all of the following are true:

1. A prompt sent via `nexus chat` is classified and dispatched to the correct layer in **<500ms orchestrator classification time** (excluding LLM inference time).
2. If a layer's Ollama model is unresponsive, **fallback activates within the configured timeout** and a structured warning is written to the log.
3. A CLI session interrupted mid-conversation can be **resumed with full context** via `nexus chat --session <id>`.
4. An episode is **written to SQLite on session close**; if the Nexus process is killed mid-session, restarting and resuming the session replays correctly from the last persisted event.
5. Continue or Cursor pointed at `http://localhost:<port>/v1/chat/completions` with an API key **works without any client-side modification**.
6. Changing any layer model, orchestrator rule, or governance rule in `config.yaml` and restarting Nexus **takes effect with no code change**.
7. `nexus status` accurately reflects the real-time health of all three layers, showing whether each is backed by Ollama or a cloud provider and its current availability.
8. The Prometheus `/metrics` endpoint is scrapable and includes per-layer request counts and latency histograms.

---

## 7. Technical Constraints

| Constraint | Decision |
|---|---|
| Language | Go. Mono repo. No other backend language. |
| Model runtime (v1) | Layer 1 and Layer 2: Ollama only. Layer 3: Ollama OR a supported cloud provider — configurable per install. |
| Layer 1 default model | `llama3.2:3b` via Ollama (configurable) |
| Layer 2 default model | `llama3.1:8b` via Ollama (configurable) |
| Layer 3 default model | `llama3.3:70b` via Ollama if hardware supports it; otherwise configure `provider: anthropic | google | openai` with an API key. Both paths work identically from the routing layer's perspective. |
| Cloud providers (v1) | `anthropic` (Claude), `google` (Gemini), `openai` (GPT-4o) — supported for Layer 3 only. Cloud API key stored in YAML config. No cloud dependency if Layer 3 is set to Ollama. |
| External dependencies | None required if all layers use Ollama. Cloud API key needed only if Layer 3 is configured as a cloud provider. |
| Binary | Single static Go binary. No separate runtime processes. |
| OS targets | macOS arm64 + amd64 (v1 release). Linux amd64 (CI-tested in v1, release target in v2). |
| Session file format | SQLite (consistent with episodic store). |
| Vector index (Tier 1) | `sqlite-vec` — pure Go, no CGo, ships inside the single binary. |
| HTTP router | `chi` or `net/http` standard library only — no heavy framework. |
| Logging | `log/slog` (standard library). |

---

## 8. Configuration Schema (v1)

```yaml
# ~/.nexus/config.yaml

server:
  port: 11434          # REST API port
  local_dev: false     # if true, API auth is bypassed on localhost

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
    # Option A — large local model (requires capable hardware)
    provider: ollama
    base_url: http://localhost:11434
    model: llama3.3:70b
    timeout: 60s
    fallback_to: null    # no fallback from layer3; return error

    # Option B — cloud provider (use when hardware cannot run a 70B model)
    # provider: anthropic          # anthropic | google | openai
    # model: claude-sonnet-4-6     # model ID for the chosen provider
    # api_key: ""                  # or set NEXUS_LAYER3_API_KEY env var
    # timeout: 30s
    # fallback_to: null

orchestrator:
  strategy: keyword      # keyword only in v1
  rules:
    - pattern: "(what is|define|explain briefly)"
      layer: layer1
    - pattern: "(write|implement|refactor|debug)"
      layer: layer2
    - pattern: "(architect|design|analyse entire|compare at length)"
      layer: layer3
  default_layer: layer2  # if no rule matches

governance:
  input:
    enabled: true
    rules:
      - name: pii_email
        pattern: '\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b'
        action: block
      - name: prompt_injection
        pattern: "ignore previous instructions"
        action: block
  output:
    enabled: true
    max_tokens: 4096
    disallowed_patterns: []
  cost:
    max_tokens_per_request: 8192
    warn_session_tokens: 100000
    block_session_tokens: 0    # 0 = no block

memory:
  working:
    ttl: 30m
  episodic:
    db: ~/.nexus/episodes.db
    open_episode_timeout: 72h
    extraction_strategy: keyword
    vector_index: sqlite-vec
  semantic:             # stub in v1; no-op
    enabled: false
  archival:             # stub in v1; no-op
    enabled: false

logging:
  level: info           # debug | info | warn | error
  format: json          # json | text
  output: stdout        # stdout | /path/to/file

metrics:
  enabled: true
  path: /metrics
```

---

## 9. v2 Roadmap (out of scope for this document)

- Cloud providers for Layer 1 and Layer 2 (v1 supports cloud for Layer 3 only)
- Semantic Store (Tier 2): SQLite document graph + vector index
- Archival / RAG (Tier 3): pluggable vector store
- Tools and Skills registry with plugin binary support
- Coding sub-agent: PM → Analyst → Architect → Developer → Tester SDLC workflow
- SvelteKit dashboard embedded in binary
- OpenTelemetry end-to-end tracing
- Embedding-based and LLM-based orchestrator classifiers
- JWT auth and multi-user access control
- llama-guard integration for governance
- Linux amd64 release target
