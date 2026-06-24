Here is my idea: I want to develop an AI agent harness called 'nexus' — similar to Copilot CLI or Claude CLI — with the following features:

1. **Access**
   Nexus should be accessible via CLI and a REST API. The REST API must satisfy two distinct requirements:
   - **OpenAPI-documented**: the API surface is described by an OpenAPI (Swagger) spec so clients can auto-generate SDKs.
   - **OpenAI-compatible**: the chat endpoint implements the `/v1/chat/completions` request/response contract so that existing tools (Continue, Cursor, litellm, etc.) can point at Nexus as a drop-in backend without modification.
   Authentication should be enforced via API key or JWT on the REST layer, with per-key rate limiting.

2. **3-Layer Hierarchical Routing**
   Nexus uses three routing layers. The routing *rules* are deterministic (given a classified intent, the rule that maps it to a layer is fixed), but the classification step itself may use a lightweight model.
   - **Layer 1 — Fast local**: a small, low-latency local model (e.g. Ollama with a 3B–7B model). Handles simple, well-scoped prompts that need no deep reasoning. Target: <500ms response.
   - **Layer 2 — Quality local**: a larger local model (e.g. Ollama with a 13B–70B model). Handles moderately complex prompts where accuracy matters but cloud cost or privacy is a concern.
   - **Layer 3 — Cloud**: a cloud LLM (Gemini, Claude, Copilot, etc.). Reserved for prompts requiring frontier reasoning, tool use, or tasks the local models could not handle.
   All three layers are fully configurable via a YAML file. Any layer can be pointed at a local Ollama endpoint or a cloud provider.
   If Layer 3 is unavailable (rate-limited or down), Nexus falls back to Layer 2 and logs a warning. Fallback behaviour is configurable per-layer.
   Local Ollama models are pre-warmed on Nexus startup for the layers that use them. Warm-up timeout is configurable; on timeout, the layer is marked unavailable and fallback activates.

3. **Orchestrator**
   The orchestrator is a lightweight local classifier — not a full LLM — that runs before any request reaches a routing layer. This avoids the paradox of sending every request to the cloud in order to decide whether to send it to the cloud.
   The orchestrator classifies the incoming prompt into an intent category, then applies the configured routing rule for that category to select a layer. The intent-to-layer mapping is defined in YAML and is fully deterministic once a category is assigned.
   Classification strategy is configurable:
   - **Keyword matching**: fast regex/keyword rules, no model needed.
   - **Embedding classifier**: a local embedding model scores similarity against intent examples.
   - **LLM classification**: sends the prompt to a small local model to classify; used only when higher accuracy is needed.
   The orchestrator model (if used) is independently configurable from the routing-layer models.

4. **Governance Layer**
   The governance layer is a middleware component positioned at two points in the request lifecycle:
   - **Pre-LLM (input guard)**: runs after the orchestrator selects a layer but before the prompt is sent to the LLM. Checks for policy violations, prompt injection, sensitive data exfiltration attempts, and PII.
   - **Post-LLM (output guard)**: runs after the LLM returns a response. Checks for harmful content, hallucinated credentials, or policy-violating output before it reaches the caller.
   Guardrail models are configurable (e.g. Ollama's llama-guard or any compatible model). On rejection:
   - Input rejection: return a structured error to the caller; do not invoke the LLM.
   - Output rejection: either return a sanitised version or return a structured error; configurable per policy rule.
   The governance layer also enforces cost guardrails: per-request token budget caps and per-user/per-key daily spend limits. Budget overruns are logged and can optionally block the request.

5. **Memory Management**
   Nexus maintains four tiers of memory. Promotion between tiers is explicit and rule-driven, not implicit. The four tiers are: Working Context, Episodic, Semantic, and Archival.

   - **Tier 0 — Working Context**: the in-flight message buffer, active tool call state, and the current orchestrator routing decision. Scoped to a single session; stored in RAM and optionally flushed to a session file for resume. Automatically discarded when the session ends or a configurable TTL expires. An episode (Tier 1) is opened automatically when a task or session begins.

   - **Tier 1 — Episodic Store**: a causally ordered log of *what happened and when*. An episode is the unit of cross-session continuity — it stays open across session boundaries until explicitly closed or a configurable timeout expires. Each episode records an ordered, causal event log (orchestrator routing decisions, governance outcomes, LLM calls, tool calls, agent role transitions, memory reads/writes). On close, a lightweight extractor writes key facts to the Semantic Store. Episodes are stored in a local SQLite database (`~/.nexus/episodes.db`) with three retrieval modes: temporal (by time range), structural (by episode type or outcome), and semantic (cosine similarity on a per-episode embedding generated from its auto-summary). The coding sub-agent workflow is persisted entirely as an episode, enabling crash recovery by replaying from the last recorded event.

   - **Tier 2 — Semantic Store**: a structured knowledge store of *what is known* — facts, user preferences, past decisions, and domain context extracted from closed episodes or written directly by agents. Stored as a document graph in SQLite with a local vector index for similarity lookup. Replaces the previous "medium-term" tier; the repetition-count promotion heuristic is removed in favour of explicit extraction on episode close.

   - **Tier 3 — Archival / RAG**: a RAG pipeline. Documents, code, high-confidence facts, and compressed episode summaries are chunked, embedded, and indexed in a vector store. Retrieval is similarity-based. Archival is the only tier that persists across Nexus restarts by default. Tiers 0–2 can be optionally persisted via configuration.

   **Promotion rules:**
   - Working Context → Episodic: automatic on task/session start.
   - Episodic → Semantic: triggered on episode close; a configurable extractor (keyword rules or small local model) derives facts and writes them to the Semantic Store.
   - Episodic → Archival: explicit commit action, or episode age > T days (T configurable).
   - Semantic → Archival: explicit commit action, or document age > T days without invalidation (T configurable).
   - Invalidation: any tier can mark a fact or episode stale; stale entries are excluded from retrieval and eligible for deletion.

   **Orchestrator integration:** before routing a request, the orchestrator queries the Episodic Store for the effective layer used in recent episodes matching the same intent category and user. If a higher layer was consistently reached after fallback, the orchestrator skips lower layers directly, without a cloud call.

   **YAML configuration:**
   ```yaml
   memory:
     working:
       ttl: 30m
     episodic:
       db: ~/.nexus/episodes.db
       open_episode_timeout: 72h
       extraction_strategy: keyword   # keyword | llm
       extraction_model: ""           # only if strategy: llm
     semantic:
       db: ~/.nexus/semantic.db
       vector_index: faiss            # faiss | sqlite-vec
     archival:
       vector_store: chroma           # chroma | weaviate | pgvector
       persist_on_restart: true
   ```

6. **Skills and Tools Registry**
   Nexus supports a plugin system with two registries:
   - **Tools registry**: discrete functions or executables an agent can invoke (e.g. web search, code execution, file read/write). Each tool implements a defined Go interface and is registered by name.
   - **Skills registry**: higher-level, multi-step capabilities composed of tools and agent calls (e.g. "summarise this codebase", "run the test suite and report failures"). Skills are defined as composable pipelines.
   Both registries are extensible at runtime via config or plugin binaries. Each registered tool/skill is automatically exposed as a CLI subcommand and a REST endpoint.
   Plugin contract: every agent, sub-agent, tool, and skill must implement a versioned Go interface (`Agent`, `Tool`, or `Skill`) defined in the `nexus/sdk` package. This is the only extension point; there is no other way to add capabilities.

7. **Coding Sub-Agent**
   The coding sub-agent takes a natural-language requirement through a full SDLC workflow. The workflow engine is written in Go. The default implementation language for generated scripts and tools is TypeScript.
   **Roles and workflow:**
   - **Project Manager**: breaks the requirement into tasks, maintains a task backlog, tracks state, and coordinates handoffs. This is a distinct role from the Nexus orchestrator.
   - **Analyst**: clarifies ambiguous requirements, produces an acceptance criteria document.
   - **Architect**: produces a technical design; reviews developer output and approves or rejects before it proceeds.
   - **Developer**: implements the design. If the Architect rejects, the Developer revises. Maximum revision cycles between Developer and Architect: configurable, default 3. After the maximum, the task is escalated back to the Project Manager.
   - **Tester**: runs tests against the approved implementation. If tests fail, the task returns to the Developer with the failure report. Maximum test-fail cycles: configurable, default 3. After the maximum, the task is escalated to the Project Manager.
   **Inputs:**
   - A natural-language requirement (required).
   - An optional Git repository URL. If provided, the coding agent clones the repository, analyses the existing codebase structure, extracts conventions and patterns, and uses this as context for all role agents so that generated code is consistent with the existing project. The agent also targets the cloned repo for all file writes and can open a pull request on completion.
   **Output:** working code committed to a branch, a test report, and an architect-approved design document.

8. **Platform: Go Mono Repo**
   Nexus is built as a Go mono repo. Structure:
   - `nexus/cmd` — CLI entry points for the harness and each agent.
   - `nexus/api` — REST API server (OpenAPI-documented, OpenAI-compatible chat endpoint).
   - `nexus/orchestrator` — routing classifier and layer dispatch.
   - `nexus/governance` — input/output guardrail middleware.
   - `nexus/memory` — short/medium/long-term memory implementations.
   - `nexus/registry` — tools and skills registries.
   - `nexus/agents` — each sub-agent (coding agent, etc.) as a self-contained package.
   - `nexus/sdk` — versioned plugin interfaces (`Agent`, `Tool`, `Skill`).
   Every agent and sub-agent is accessible both as a CLI subcommand (`nexus coding-agent --requirement "..."`) and as a REST endpoint (`POST /v1/agents/coding`). New agents are added by implementing the `sdk.Agent` interface and registering in the mono repo — no forking required.

9. **Observability**
   Every step in the orchestrator, governance layer, memory reads/writes, tool calls, and agent role transitions is instrumented. Observability includes:
   - **Structured logging**: JSON logs with trace ID, session ID, layer selected, model used, latency, and token count per request.
   - **Metrics**: Prometheus-compatible counters and histograms for request counts, routing distribution, latency by layer, governance rejections, and cost estimates.
   - **Tracing**: OpenTelemetry spans across the full request lifecycle (orchestrator → governance → LLM → memory → response).
   - **Cost tracking**: per-request estimated cost (tokens × per-model rate) logged and accumulated per session, per user, and per agent. Exportable as a daily report.
   The observability backend (log sink, metrics scrape endpoint, trace exporter) is configurable. Defaults to stdout logging and a local Prometheus `/metrics` endpoint with no external dependency required.

10. **Dashboard (SvelteKit)**
    Nexus ships a lightweight browser-based dashboard for observability and registry inspection. It is built with SvelteKit, compiled to static assets, and embedded directly in the Go binary via `embed.FS`. No separate Node.js process or external service is required at runtime. The dashboard is served by the Nexus API server at `http://localhost:<port>/ui`.

    **Scope — what the dashboard shows:**
    - **Live request feed**: a streaming view of incoming requests, showing session ID, intent classification, layer selected, model used, latency, token count, and governance decisions. Updates via Server-Sent Events (SSE) from the API server; no page refresh required.
    - **Trace inspector**: click any request in the live feed to expand its full OpenTelemetry span tree — orchestrator → governance → LLM call → memory reads/writes → response.
    - **Metrics panel**: charts for request volume, routing distribution (% to each layer), latency percentiles (p50/p95/p99), governance rejection rate, and estimated cost per hour. Data polled from the Nexus `/v1/dashboard/metrics` endpoint on a configurable interval (default: 10s).
    - **Cost tracker**: cumulative spend by session, by agent, and by user/API key. Displays against configured budget caps with a visual warning when approaching limits.
    - **Registry browser**: read-only view of all registered agents, tools, and skills. Shows name, version, description, CLI command, and REST endpoint for each. No install/uninstall from the UI.
    - **Memory inspector**: read-only view of the three memory tiers. Browse medium-term document graph nodes; view long-term RAG document count and index statistics. Does not expose raw short-term session buffers.

    **What the dashboard does NOT do:**
    - No YAML config editing from the UI. Configuration remains file-based.
    - No agent start/stop controls. Agents are managed via CLI or REST API.
    - No dependency on Grafana, Loki, Tempo, or any external observability backend.

    **API surface (new endpoints added to the Nexus API server for the dashboard):**
    - `GET /v1/dashboard/metrics` — aggregated metrics snapshot (JSON).
    - `GET /v1/dashboard/sessions` — recent session list with summary stats.
    - `GET /v1/dashboard/sessions/{id}/trace` — full span tree for a session.
    - `GET /v1/dashboard/registry` — all registered agents, tools, skills.
    - `GET /v1/dashboard/memory/summary` — memory tier statistics.
    - `GET /v1/dashboard/stream` — SSE endpoint for live request events.

    **Authentication:**
    Same API key or JWT as the REST API, passed as a `Bearer` token. When Nexus runs in local development mode (`--local-dev` flag), the dashboard is accessible on `localhost` without credentials. In any other mode, auth is required.

    **Build and deployment:**
    The SvelteKit app lives at `nexus/ui` in the mono repo. It is built separately (`pnpm build`) and the output is embedded into the Go binary at compile time. The Go build pipeline includes a `go generate` step that runs the SvelteKit build before compiling. No separate deployment step; the dashboard ships as part of the single Nexus binary.
