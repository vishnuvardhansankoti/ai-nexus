# Nexus — Memory Architecture Design

## Overview

Nexus uses a four-tier memory architecture. The tiers form a pipeline from ephemeral in-session state through to a persistent archival store. Promotion between tiers is explicit and rule-driven.

```
┌─────────────────────────────────────────────────────────┐
│  Tier 0 · Working Context  (RAM + session file)         │
│  In-flight message buffer, active tool state,           │
│  current orchestrator routing decision                  │
│  TTL: session-scoped. Resumable via session file.       │
└───────────────────────┬─────────────────────────────────┘
                        │ episode opens on task start
                        ▼
┌─────────────────────────────────────────────────────────┐
│  Tier 1 · Episodic Store  (SQLite: episodes + events)   │
│  Ordered, causal log of what happened and when          │
│  Survives crashes. Spans multiple sessions.             │
│  Retrieval: temporal | structural | semantic (summary)  │
└───────────────────────┬─────────────────────────────────┘
                        │ fact extraction on episode close
                        ▼
┌─────────────────────────────────────────────────────────┐
│  Tier 2 · Semantic Store  (SQLite + vector index)       │
│  "What is known" — facts, preferences, decisions        │
│  Knowledge graph nodes + similarity retrieval           │
└───────────────────────┬─────────────────────────────────┘
                        │ age > T days or explicit commit
                        ▼
┌─────────────────────────────────────────────────────────┐
│  Tier 3 · Archival / RAG  (vector store)                │
│  Chunked docs, code, high-confidence facts,             │
│  compressed episode summaries                           │
│  Persists across restarts. Similarity retrieval only.   │
└─────────────────────────────────────────────────────────┘
```

---

## Tier 0 — Working Context

**What it stores:** the live conversation buffer, active tool call state, and the orchestrator's current routing decision for the in-flight request.

**Storage:** in-process memory (Go structs). Optionally flushed to a session file (`~/.nexus/sessions/<id>.db`) on each turn so sessions can be resumed across CLI invocations.

**Lifecycle:** opened when a session starts, discarded when the session ends or TTL expires.

**Key property:** Working Context is not a memory tier in the retrieval sense — it is the agent's current working set. It feeds Tier 1 as a side-effect of task execution.

---

## Tier 1 — Episodic Store

### What an episode is

An **episode** is the primary unit of cross-session continuity. It captures a bounded interaction — a task, a coding workflow, a multi-turn diagnostic conversation — as an ordered, causal sequence of events.

An episode:
- Opens automatically when a session starts a task.
- Stays open across session boundaries (a coding task can span Monday–Friday).
- Closes when the task completes, the user explicitly closes it, or `open_episode_timeout` elapses.
- On close, a lightweight extractor runs to derive facts for Tier 2.

### Data model

```
Episode {
  id            UUID
  title         string          // auto-generated on close from summary
  status        open | closed | failed
  created_at    timestamp
  closed_at     timestamp
  session_ids   []string        // all sessions that touched this episode
  tags          []string        // intent category, agents involved, project
  outcome       success | failure | partial | escalated
  summary       string          // generated on close
  embedding     []float32       // cosine index on summary
}

Event {
  id            UUID
  episode_id    UUID
  parent_id     UUID            // causal parent, not just time-order
  timestamp     timestamp
  type          enum {
                  orchestrator_route,
                  governance_reject,
                  governance_pass,
                  llm_call,
                  llm_response,
                  tool_call,
                  tool_response,
                  agent_transition,
                  memory_read,
                  memory_write,
                  episode_open,
                  episode_close,
                }
  layer         int             // 1 | 2 | 3 | 0 (local/governance)
  model         string
  latency_ms    int
  tokens_in     int
  tokens_out    int
  payload       JSON
}
```

### Retrieval modes

The episodic store exposes three query interfaces:

| Mode | API | Example use |
|---|---|---|
| Temporal | `QueryByTime(from, to time.Time)` | "What ran last Tuesday?" |
| Structural | `QueryByFilter(type, outcome, agent)` | "Find coding episodes that escalated" |
| Semantic | `QueryBySimilarity(embedding, k int)` | "Find episodes similar to this prompt" |

All three modes are exposed on the `nexus/memory` Go interface and through the dashboard Memory Inspector.

### Storage

SQLite at `~/.nexus/episodes.db` with two tables (`episodes`, `events`) and FTS5 for text search on `summary`. A secondary vector index (configurable: `faiss` or `sqlite-vec`) is built on `Episode.embedding` for semantic retrieval.

### Crash recovery for the coding sub-agent

The coding sub-agent writes every role transition (PM → Analyst, Architect reject, Developer revision, etc.) as an `agent_transition` event into the active episode. On restart, the coding agent loads the open episode and replays from the last recorded event. No workflow state is held only in process memory.

---

## Tier 2 — Semantic Store

**What it stores:** structured knowledge — facts, user preferences, past decisions, domain context. This is the "what is known" layer, as opposed to Tier 1's "what happened" layer.

**Source:** facts are written here by two paths:
1. The episode extractor runs on episode close and writes derived facts.
2. Agents can write facts directly via the `memory_write` action.

**Storage:** SQLite at `~/.nexus/semantic.db` modelled as a document graph (nodes = facts/concepts, edges = typed relationships). A local vector index (`faiss` or `sqlite-vec`) enables similarity retrieval.

**Retrieval:** semantic similarity (primary) + exact key lookup for known fact IDs.

**Invalidation:** any agent or rule can mark a fact stale. Stale facts are excluded from retrieval and eligible for deletion on the next compaction run.

### Fact extraction on episode close

The extractor is configurable:

- **`keyword`** (default): regex/keyword rules identify named entities, preferences, and decisions. Zero additional model cost.
- **`llm`**: sends the episode summary to a small local model with a structured extraction prompt. Higher accuracy, adds latency on close.

The extractor runs asynchronously — episode close is not blocked on extraction completion.

---

## Tier 3 — Archival / RAG

**What it stores:** chunked documents, source code, high-confidence facts committed from Tier 2, and compressed episode summaries committed from Tier 1.

**Storage:** a configurable vector store (`chroma`, `weaviate`, or `pgvector`). This is the only tier that persists across Nexus restarts by default.

**Retrieval:** cosine similarity only. No temporal or structural queries.

**Promotion into Archival:**
- From Tier 1: explicit commit, or episode age > T days (configurable).
- From Tier 2: explicit commit, or fact age > T days without invalidation.

---

## Promotion Rules Summary

| From | To | Trigger |
|---|---|---|
| Tier 0 Working Context | Tier 1 Episodic | Automatic on task/session start |
| Tier 1 Episodic | Tier 2 Semantic | Episode close → extractor writes facts |
| Tier 1 Episodic | Tier 3 Archival | Explicit commit or age > T days |
| Tier 2 Semantic | Tier 3 Archival | Explicit commit or age > T days without invalidation |

---

## Orchestrator Integration

Before routing a request, the orchestrator queries Tier 1 for recent closed episodes matching the same intent category and user/session context. If a higher layer was consistently reached after fallback (e.g., Layer 1 timed out and Layer 3 was the effective responder in the last K episodes), the orchestrator routes directly to the higher layer, skipping the lower-layer probe.

This is the only place where routing adapts from past experience, and it does so without a cloud call.

---

## Dashboard — Memory Inspector

The dashboard's Memory Inspector provides read-only views of Tiers 1–3:

- **Episodic**: browse open and closed episodes, event log per episode, outcome distribution.
- **Semantic**: browse document graph nodes and edges, fact count, index statistics.
- **Archival**: document count, index statistics, last commit timestamp.

Raw Tier 0 (session buffer) is not exposed in the dashboard.

---

## YAML Configuration Reference

```yaml
memory:
  working:
    ttl: 30m                         # session TTL before discard

  episodic:
    db: ~/.nexus/episodes.db
    open_episode_timeout: 72h        # max lifespan for an open episode
    extraction_strategy: keyword     # keyword | llm
    extraction_model: ""             # only used when strategy: llm
    vector_index: faiss              # faiss | sqlite-vec
    persist: true                    # persist across restarts

  semantic:
    db: ~/.nexus/semantic.db
    vector_index: faiss              # faiss | sqlite-vec
    persist: true

  archival:
    vector_store: chroma             # chroma | weaviate | pgvector
    persist_on_restart: true
    promotion_age_days: 30           # auto-promote facts/episodes older than this
```

---

## Go Package Structure

```
nexus/memory/
  working/      # Tier 0: in-process buffer + session file I/O
  episodic/     # Tier 1: SQLite episodes+events, extractor, vector index
  semantic/     # Tier 2: SQLite document graph, vector index
  archival/     # Tier 3: vector store adapter (chroma/weaviate/pgvector)
  interfaces.go # shared Memory, Episode, Fact, Query interfaces
```

All tiers implement the same base `nexus/sdk.MemoryStore` interface so the orchestrator, agents, and governance layer interact with memory through a single contract regardless of which tier they are reading from or writing to.
