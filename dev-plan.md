# Nexus — Development Plan

## Overview

Two-phase delivery: v1 (Phases 0–5, ~14 weeks) ships a fully local, production-usable AI routing layer with CLI and OpenAI-compatible REST API. v2 (Phases 6–10) adds cloud providers, full memory tiers, the coding sub-agent, and the dashboard.

All constraints, scope, and success metrics are defined in `prd.md`. All features are built in Go. The mono repo structure follows `idea.md` section 8.

---

## Repository Structure

```
nexus/
  cmd/
    nexus/          # main binary entry point
  api/              # REST API server
  orchestrator/     # classifier + layer dispatch
  governance/       # input/output guard middleware
  memory/
    working/        # Tier 0: in-process buffer + session file
    episodic/       # Tier 1: SQLite episodes + events
    semantic/       # Tier 2: stub in v1
    archival/       # Tier 3: stub in v1
  providers/
    ollama/         # Ollama HTTP client
    cloud/          # cloud adapter interface (no impl in v1)
  config/           # YAML loader + struct validation
  registry/         # tools + skills registries (v2)
  agents/
    coding/         # coding sub-agent (v2)
  sdk/              # versioned plugin interfaces
  ui/               # SvelteKit dashboard (v2)
  Makefile
  openapi.yaml
  .github/
    workflows/
      ci.yml
```

---

## v1 — Phases 0–5 (Weeks 1–14)

### Phase 0 — Repository Scaffold (Week 1)

**Goal:** A compiling mono repo with all packages stubbed, interfaces defined, and CI green.

**Deliverables:**
- `go.mod` with module path `github.com/<user>/nexus`
- Directory skeleton (all packages above created as empty packages with a single `.go` file)
- `nexus/sdk` — versioned interfaces:
  ```go
  // sdk/interfaces.go
  type Agent interface { ... }
  type Tool interface  { ... }
  type Skill interface { ... }
  type MemoryStore interface { Query(...) / Write(...) }
  type Provider interface { Chat(ctx, req) (Response, error) }
  ```
- `nexus/config` — YAML loader using `gopkg.in/yaml.v3`; struct mirrors `prd.md` section 8 config schema; validation via `go-playground/validator`
- `Makefile` targets: `build`, `test`, `lint`, `generate`, `clean`
- `.github/workflows/ci.yml`: `go build ./...`, `go test ./...`, `golangci-lint run` on push to main and all PRs
- `openapi.yaml` skeleton with `POST /v1/chat/completions`, `GET /healthz`, `GET /metrics`, `GET /v1/models`

**Done when:** `make build` produces a binary; `make test` passes (no tests yet, just compilation); CI is green.

---

### Phase 1 — 3-Layer Routing + CLI (Weeks 2–5)

**Goal:** A working `nexus chat` command that routes prompts to the correct Ollama layer, with fallback and pre-warm.

#### 1a — Providers: Ollama + Cloud (Week 2)

**Ollama provider** (`nexus/providers/ollama`):
- HTTP client wrapping the Ollama `/api/chat` endpoint
- Implements `sdk.Provider`
- Handles streaming responses (SSE from Ollama → assembled for non-streaming callers)
- Connection pooling; configurable timeout per layer

**Cloud providers** (`nexus/providers/cloud`):
Layer 3 must support cloud backends for machines that cannot run a 70B model locally. All three adapters are implemented in v1 and selected via `provider:` in the Layer 3 YAML config.

- `nexus/providers/cloud/anthropic` — Claude adapter (Anthropic Messages API)
  - Model configurable (e.g. `claude-sonnet-4-6`, `claude-haiku-4-5`)
  - API key from YAML `api_key` field or `NEXUS_LAYER3_API_KEY` env var
  - Streaming via Anthropic SSE format → normalized to internal `Response`
- `nexus/providers/cloud/google` — Gemini adapter (Google AI Studio / Vertex)
  - Model configurable (e.g. `gemini-2.0-flash`, `gemini-1.5-pro`)
- `nexus/providers/cloud/openai` — OpenAI adapter
  - Model configurable (e.g. `gpt-4o`, `gpt-4o-mini`)
  - Also usable against any OpenAI-compatible endpoint (e.g. local vLLM serving a 70B model)

All three implement `sdk.Provider` identically. The provider factory in `nexus/providers/factory.go` reads the layer config's `provider:` field and constructs the correct implementation at startup.

Cloud Layer 3 startup validation: instead of a warm-up inference, Nexus calls a lightweight model-list or ping endpoint to confirm the API key is valid and the model exists. Failure marks Layer 3 unavailable and activates fallback (which in practice means returning a structured error, since Layer 3 has no configured fallback).

#### 1b — Orchestrator + Layer Dispatch (Weeks 2–3)

- `nexus/orchestrator`:
  - Keyword classifier: compiles YAML `rules[].pattern` to `*regexp.Regexp` at startup
  - `Classify(prompt string) IntentCategory` — returns the matched rule's layer or `config.DefaultLayer`
  - `Dispatch(ctx, category, request)` — selects the provider for the category's layer; on failure triggers fallback chain
  - Fallback chain: `layer.fallback_to` is followed until a layer responds or the chain is exhausted
  - Structured warning log on each fallback hop: `{"event":"fallback","from":"layer1","to":"layer2","reason":"timeout"}`
- Ollama pre-warm on startup: send a minimal `/api/chat` request to each configured model; mark layer available/unavailable based on response within timeout

#### 1c — Working Context + Session File (Week 3)

- `nexus/memory/working`:
  - In-process `[]Message` buffer (role + content)
  - `SessionStore`: reads/writes `~/.nexus/sessions/<id>.db` (SQLite, single `messages` table)
  - On each turn: append to buffer AND flush to SQLite (atomic write)
  - `Load(sessionID)` restores the buffer from SQLite on `nexus chat --session <id>`

#### 1d — CLI (Weeks 4–5)

- `nexus/cmd/nexus` — entry point using `cobra`
- `nexus chat`: interactive REPL (prompt → orchestrator → provider → print response → repeat) and single-shot (`--prompt "..."`)
  - `--session <id>` flag: resumes an existing session
  - New sessions auto-generate a UUID session ID printed on start
  - `Ctrl+C` / `exit` closes the session and writes the episode close event
- `nexus status`: queries each layer's Ollama model availability (HEAD request or minimal chat); prints a table with layer, model, status, last-latency-ms
- `nexus config validate`: loads `~/.nexus/config.yaml`, runs validation, prints errors or "OK"
- `nexus config show`: prints the resolved config as JSON
- `nexus version`: prints version string and build-time commit hash (injected via `-ldflags`)

**Done when:** `nexus chat --prompt "what is 2+2"` routes to Layer 1, returns a response, and the session file exists at `~/.nexus/sessions/<id>.db`.

---

### Phase 2 — Episodic Store (Weeks 6–8)

**Goal:** Every request lifecycle is recorded as a causal event log in SQLite; episodes survive crashes and span sessions.

#### Schema

```sql
CREATE TABLE episodes (
  id TEXT PRIMARY KEY,
  title TEXT,
  status TEXT NOT NULL,  -- open | closed | failed
  created_at INTEGER NOT NULL,
  closed_at INTEGER,
  session_ids TEXT NOT NULL,  -- JSON array
  tags TEXT,                  -- JSON array
  outcome TEXT,               -- success | failure | partial | escalated
  summary TEXT,
  embedding BLOB              -- sqlite-vec float32 array (populated on close)
);

CREATE TABLE events (
  id TEXT PRIMARY KEY,
  episode_id TEXT NOT NULL REFERENCES episodes(id),
  parent_id TEXT,
  timestamp INTEGER NOT NULL,
  type TEXT NOT NULL,
  layer INTEGER,
  model TEXT,
  latency_ms INTEGER,
  tokens_in INTEGER,
  tokens_out INTEGER,
  payload TEXT  -- JSON
);

CREATE VIRTUAL TABLE episodes_fts USING fts5(title, summary, content=episodes);
```

#### Deliverables

- `nexus/memory/episodic`:
  - `OpenEpisode(sessionID)` — creates episode row, writes `episode_open` event
  - `AppendEvent(episodeID, Event)` — inserts event row; called by orchestrator, governance, and provider after every action
  - `CloseEpisode(episodeID, outcome)` — sets status=closed, runs keyword extractor, writes summary (static placeholder in v1 since no LLM call for summarisation), writes `episode_close` event
  - `QueryByTime(from, to time.Time)` — returns episodes in range
  - `QueryByFilter(status, outcome string)` — structural query
  - `sqlite-vec` index built on `embedding` column (embedding populated via Ollama embedding model in v2; placeholder zeros in v1)
- Orchestrator writes `orchestrator_route` event on every dispatch
- Provider client writes `llm_call` and `llm_response` events (including latency and token counts from Ollama response)
- Governance writes `governance_reject` or `governance_pass` event
- On `nexus chat` startup: if `--session` flag is provided and the episode for that session is `open`, the episode is resumed (new events appended); if no flag, a new episode is opened
- `open_episode_timeout` background goroutine: scans for episodes older than timeout and marks them `failed`

**Done when:** Kill `nexus chat` mid-session, restart with `nexus chat --session <id>`, verify events table has both the pre-kill and post-restart events in a single episode.

---

### Phase 3 — Governance Layer (Weeks 9–10)

**Goal:** A middleware that sits between the orchestrator and the provider, filtering both prompt and response.

- `nexus/governance`:
  - `Middleware` interface: `CheckInput(ctx, prompt) error` and `CheckOutput(ctx, response) error`
  - Input guard: iterate over `config.Governance.Input.Rules`; compile each pattern to `*regexp.Regexp`; first match returns a `GovernanceError{Code: "blocked", Rule: ruleName, Reason: ...}`
  - Output guard: check `len(tokens) > config.Governance.Output.MaxTokens`; check disallowed patterns
  - Cost guard: accumulate `tokens_in + tokens_out` per session in a lightweight in-memory map (session ID → running total); compare against `config.Governance.Cost` thresholds; log warning at `warn_session_tokens`; optionally block at `block_session_tokens` (0 = disabled)
  - On rejection: return `{"error": {"code": "governance_blocked", "rule": "...", "message": "..."}}`; do not invoke the LLM
  - Write `governance_reject` or `governance_pass` event to episodic store after every check

**Done when:** A prompt matching a PII regex is blocked before reaching Ollama; the governance event appears in the episode; the structured error is returned to the caller.

---

### Phase 4 — REST API (Weeks 11–13)

**Goal:** An HTTP server that makes Nexus usable as a drop-in backend for Continue, Cursor, and litellm.

- `nexus/api`:
  - HTTP server using `net/http` + `chi` router
  - Auth middleware: reads `Authorization: Bearer <key>` header; validates against `config.Auth.APIKeys`; rejects with 401 on mismatch
  - Rate limiter middleware: per-API-key sliding window using an in-memory token bucket; returns 429 on limit exceeded
  - `POST /v1/chat/completions` — OpenAI-compatible handler:
    - Accepts `{"model": "...", "messages": [...], "stream": bool}`
    - Maps `model` field to a layer (or uses orchestrator if model is `"auto"`)
    - Routes through governance → orchestrator → provider
    - Non-streaming: returns `{"choices": [{"message": {"role": "assistant", "content": "..."}}], "usage": {...}}`
    - Streaming: returns `text/event-stream` with `data: {"choices": [{"delta": {"content": "..."}}]}\n\n` chunks
  - `GET /v1/models` — returns configured layers as model list entries (`{"id": "nexus-layer1", "object": "model", ...}`)
  - `GET /healthz` — returns `{"status": "ok"}` or `{"status": "degraded", "layers": {...}}`
  - `GET /metrics` — Prometheus text format
  - `nexus serve` CLI command: starts the HTTP server; prints the listening address

**Done when:** A `curl` to `POST /v1/chat/completions` with a Bearer token returns a valid OpenAI-format response; the same request from the Continue IDE extension works without modification.

---

### Phase 5 — Observability (Week 14)

**Goal:** Every component emits structured logs and Prometheus metrics; all logs carry a trace ID.

- Trace ID middleware: generate UUID trace ID per request; store in `context.Context`; pass through orchestrator → governance → provider → memory
- `log/slog` structured logger with fields: `trace_id`, `session_id`, `layer`, `model`, `latency_ms`, `tokens_in`, `tokens_out`, `governance_decision`
- Log level and format (JSON/text) configurable via YAML
- `nexus/api/metrics.go`: Prometheus collectors using `prometheus/client_golang`:
  - `nexus_requests_total` (counter, labels: layer, model, status)
  - `nexus_request_duration_ms` (histogram, labels: layer)
  - `nexus_governance_rejections_total` (counter, labels: rule)
  - `nexus_layer_available` (gauge, labels: layer; 1=up, 0=down)
  - `nexus_tokens_total` (counter, labels: layer, direction=in|out)

**Done when:** `curl http://localhost:<port>/metrics` returns scrapable Prometheus output; every request log line contains `trace_id` and all required fields.

---

## v1 Release Checklist

- [ ] `nexus chat --prompt "..."` routes correctly to each layer based on keyword rules
- [ ] Layer fallback fires and logs warning when a layer is unavailable (Ollama timeout or cloud API error)
- [ ] Layer 3 configured as a cloud provider (anthropic/google/openai) returns a valid response via `nexus chat`
- [ ] `nexus chat --session <id>` resumes with full conversation context
- [ ] Episode crash recovery: kill + restart restores open episode
- [ ] `POST /v1/chat/completions` works from Continue / Cursor with Bearer token
- [ ] Governance blocks PII prompt; structured error returned
- [ ] `GET /metrics` returns scrapable Prometheus output
- [ ] `nexus status` shows correct layer health
- [ ] All config changes take effect on restart, no code change needed
- [ ] `make test ./...` passes; `golangci-lint` is clean
- [ ] macOS arm64 binary produced by `make build`
- [ ] Tag `v0.1.0`

---

## v2 — Phases 6–10 (Weeks 15–34)

### Phase 6 — Cloud for Layer 1/2 + Semantic Memory (Weeks 15–18)

Cloud provider adapters are already implemented in v1 for Layer 3. Phase 6 extends cloud support to Layer 1 and Layer 2, and activates the semantic memory store.

- Enable `provider: anthropic | google | openai` for Layer 1 and Layer 2 in YAML config (v1 restricts cloud to Layer 3 only)
- Per-key cloud rate limit handling with retry-after backoff; fallback activates on sustained 429
- Cloud spend tracking: estimated cost per request (tokens × per-model rate table from a configurable rates YAML) written as an `episodic` event field; accumulated per session, per user, per day

### Phase 6b — Semantic + Archival Memory (Weeks 19–22)

- `nexus/memory/semantic`: SQLite document graph (nodes + edges tables) + `sqlite-vec` index
- Keyword extractor from Phase 2 now writes real facts to semantic store (removes the v1 no-op stub)
- LLM-based extractor: opt-in via `extraction_strategy: llm`; uses the configured Layer 1 model
- `nexus/memory/archival`: `sdk.VectorStore` interface + first adapter (`chroma` via HTTP)
- Promotion rules implemented as background workers: episodic→archival on age, semantic→archival on age/commit
- `nexus memory stats` CLI command: prints tier sizes and last promotion timestamps

### Phase 7 — Tools + Skills Registry (Weeks 22–24)

- `nexus/registry`: `ToolRegistry` and `SkillRegistry` with `Register(name, impl)` and `Lookup(name)`
- Built-in tools: `web_search` (via a local search API), `file_read`, `file_write`, `code_exec` (sandboxed subprocess)
- Plugin binary loading: `nexus/registry/plugin.go` — exec plugin binary, communicate via stdin/stdout JSON-RPC
- Each registered tool auto-exposed as: `nexus tool <name>` CLI subcommand + `POST /v1/tools/<name>` REST endpoint
- Skills: composable pipelines defined in YAML referencing tool names and agent calls

### Phase 8 — Coding Sub-Agent (Weeks 25–30)

- `nexus/agents/coding`: workflow engine in Go
- Roles implemented as `sdk.Agent` implementations: `PM`, `Analyst`, `Architect`, `Developer`, `Tester`
- Every role transition written as an `agent_transition` event in the episodic store (crash recovery)
- Git clone + codebase analysis: `go-git` library; extract file tree, conventions, existing patterns as context
- PR creation: `go-github` or `gh` CLI subprocess
- Configurable limits: `architect_revision_cycles` (default 3), `test_fail_cycles` (default 3); on limit → escalate to PM
- `nexus coding-agent --requirement "..."` CLI command + `POST /v1/agents/coding` REST endpoint

### Phase 9 — Dashboard (Weeks 31–34)

- `nexus/ui`: SvelteKit app in the mono repo
- `pnpm build` compiles to `nexus/ui/dist/`; `go generate` in `nexus/api/` runs `pnpm build` then embeds `dist/` via `//go:embed`
- New API endpoints served alongside existing ones:
  - `GET /v1/dashboard/stream` — SSE; emits a JSON event per request lifecycle event
  - `GET /v1/dashboard/metrics` — aggregated metrics snapshot (JSON)
  - `GET /v1/dashboard/sessions` — recent session list
  - `GET /v1/dashboard/sessions/{id}/trace` — full event log for a session
  - `GET /v1/dashboard/registry` — registered agents, tools, skills
  - `GET /v1/dashboard/memory/summary` — tier statistics
- Dashboard served at `http://localhost:<port>/ui`
- Auth: same Bearer token as REST API; bypassed on localhost when `--local-dev` flag is set

### Phase 10 — Hardening + v2 Release (Weeks 35–37)

- OpenTelemetry tracing: replace manual trace ID propagation with `otel` spans; configure OTLP exporter
- Orchestrator: implement embedding-based classifier (`nexus/orchestrator/embedding.go`) and LLM-based classifier (`nexus/orchestrator/llm.go`); selectable via `orchestrator.strategy` in YAML
- Governance: llama-guard integration — route input/output through a local llama-guard Ollama model
- JWT auth: `nexus/api/auth/jwt.go`; per-user claims include rate limit and spend limit overrides
- End-to-end test suite: `nexus/e2e/` — spins up a mock Ollama server, exercises full request lifecycle
- Load test: `k6` script targeting `POST /v1/chat/completions`; p99 latency target < 2s (excluding LLM inference)
- Linux amd64 release target added to `Makefile` and CI
- Tag `v0.2.0`

---

## Key Dependencies

| Package | Version pin | Purpose |
|---|---|---|
| `github.com/spf13/cobra` | latest | CLI framework |
| `github.com/go-chi/chi/v5` | latest | HTTP router |
| `gopkg.in/yaml.v3` | latest | Config loading |
| `github.com/go-playground/validator/v10` | latest | Config validation |
| `github.com/mattn/go-sqlite3` | latest | SQLite (CGo; acceptable for macOS) |
| `github.com/asg017/sqlite-vec-go-bindings` | latest | Vector index on SQLite |
| `github.com/prometheus/client_golang` | latest | Prometheus metrics |
| `github.com/google/uuid` | latest | Session + episode + trace IDs |
| `golang.org/x/exp/slog` or `log/slog` | Go 1.21+ | Structured logging |
| `github.com/anthropics/anthropic-sdk-go` | latest | Claude cloud adapter (Layer 3) |
| `google.golang.org/genai` | latest | Gemini cloud adapter (Layer 3) |
| `github.com/openai/openai-go` | latest | OpenAI cloud adapter (Layer 3) |

Note: `go-sqlite3` requires CGo. This is acceptable for macOS. For a future CGo-free build, replace with `modernc.org/sqlite` (pure Go SQLite).

Note on cloud adapters: all three are compiled in unconditionally. The provider factory instantiates only the one configured for Layer 3 at startup; unused adapters add minimal binary size.

---

## Development Conventions

- Each phase must leave `make test ./...` and `golangci-lint` green before the next phase begins.
- No package may import from a sibling package at a higher level in the dependency graph. Allowed directions: `cmd` → everything; `api` → `orchestrator`, `governance`, `memory`, `providers`, `config`; `orchestrator` → `providers`, `config`; `governance` → `config`; `memory/*` → `config`; `providers/*` → `config`; `sdk` → nothing.
- All public interfaces are defined in `nexus/sdk`. Cross-package communication goes through these interfaces, not concrete types.
- Every new feature gets a unit test before the phase is marked done.
