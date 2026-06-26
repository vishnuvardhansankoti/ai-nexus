# Phase 2 Agent Brief — Episodic Store

## Context

You are implementing Phase 2 of Nexus, an AI agent harness written in Go. Work in the git repository at the current working directory. Phase 1 is already merged to `main` — the orchestrator, all three provider layers (Ollama + cloud), working memory, and the full CLI (`nexus chat`, `nexus status`, `nexus config`, `nexus version`) are operational. Your job is to implement the episodic memory store on branch `phase/2-episodic-store` and open a PR to `main`.

**Read these files before starting:**
- `sdk/interfaces.go` — canonical interfaces
- `config/config.go` — `EpisodicMemoryConfig` struct you must read from
- `orchestrator/orchestrator.go` — where you will add event hooks
- `cmd/nexus/chat.go` — where you will wire the episodic store into the CLI
- `memory/working/working.go` — reference implementation for SQLite session store pattern
- `go.mod` — current dependencies

## Locked decisions

| Decision | Value |
|---|---|
| Module path | `github.com/vishnuvardhansankoti/ai-nexus` |
| Branch | `phase/2-episodic-store` |
| PR target | `main` |
| SQLite driver | `modernc.org/sqlite` (already in go.mod) |
| UUID library | `github.com/google/uuid` (already in go.mod) |
| Episode ID format | UUID v4 |
| Timestamp precision | Unix epoch seconds for `episodes.created_at` / `closed_at`; milliseconds for `events.timestamp` |
| Summary strategy | Keyword extraction from `llm_response` payload content (v1 stub; LLM extractor in v2) |

## No new dependencies

All required packages are already in `go.mod`. Do not add any new dependencies.

---

## What to build

### 1. `memory/episodic/episodic.go` — SQLite-backed episodic store

Replace the stub. This is the primary deliverable of Phase 2.

#### Schema

Apply the following DDL at store open time:

```sql
CREATE TABLE IF NOT EXISTS episodes (
  id          TEXT    PRIMARY KEY,
  title       TEXT    NOT NULL DEFAULT '',
  status      TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  closed_at   INTEGER,
  session_ids TEXT    NOT NULL DEFAULT '[]',
  tags        TEXT    NOT NULL DEFAULT '[]',
  outcome     TEXT    NOT NULL DEFAULT '',
  summary     TEXT    NOT NULL DEFAULT '',
  embedding   BLOB
);

CREATE TABLE IF NOT EXISTS events (
  id          TEXT    PRIMARY KEY,
  episode_id  TEXT    NOT NULL REFERENCES episodes(id),
  parent_id   TEXT,
  timestamp   INTEGER NOT NULL,
  type        TEXT    NOT NULL,
  layer       INTEGER NOT NULL DEFAULT 0,
  model       TEXT    NOT NULL DEFAULT '',
  latency_ms  INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  payload     TEXT    NOT NULL DEFAULT '{}'
);

CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts
  USING fts5(title, summary, content=episodes);
```

Open the database with WAL journal mode: append `?_journal_mode=WAL` to the path when calling `sql.Open`.

#### Constants

```go
// Status values
const (
    StatusOpen   = "open"
    StatusClosed = "closed"
    StatusFailed = "failed"
)

// Outcome values
const (
    OutcomeSuccess   = "success"
    OutcomeFailure   = "failure"
    OutcomePartial   = "partial"
    OutcomeEscalated = "escalated"
)

// Event type values
const (
    TypeEpisodeOpen       = "episode_open"
    TypeEpisodeClose      = "episode_close"
    TypeOrchestratorRoute = "orchestrator_route"
    TypeLLMCall           = "llm_call"
    TypeLLMResponse       = "llm_response"
    TypeGovernancePass    = "governance_pass"
    TypeGovernanceReject  = "governance_reject"
)
```

#### Types

```go
type Episode struct {
    ID         string
    Title      string
    Status     string
    CreatedAt  int64
    ClosedAt   *int64
    SessionIDs []string
    Tags       []string
    Outcome    string
    Summary    string
}

type Event struct {
    ID        string         // auto-generated UUID if empty
    EpisodeID string
    ParentID  string
    Timestamp int64          // set to time.Now().UnixMilli() if zero
    Type      string
    Layer     int
    Model     string
    LatencyMs int64
    TokensIn  int
    TokensOut int
    Payload   map[string]any // serialised as JSON in the payload column
}

type Store struct {
    db *sql.DB
}
```

#### Constructor

```go
func New(path string) (*Store, error)
```

- `os.MkdirAll(filepath.Dir(path), 0o700)` before opening
- `sql.Open("sqlite", path+"?_journal_mode=WAL")`
- Apply the DDL schema
- Return error on any failure

```go
func DefaultDBPath() string
```

Returns `~/.nexus/episodic.db` using `os.UserHomeDir()`.

#### `OpenEpisode(sessionID string) (string, error)`

1. Generate `id = uuid.New().String()`
2. `INSERT INTO episodes (id, status, created_at, session_ids) VALUES (?, 'open', ?, ?)`
   - `session_ids` is a JSON-encoded `[]string{sessionID}`
   - `created_at` is `time.Now().Unix()`
3. Call `AppendEvent(id, Event{Type: TypeEpisodeOpen, Payload: map[string]any{"session_id": sessionID}})`
4. Return `(id, nil)` on success

#### `FindOpenEpisode(sessionID string) (string, bool, error)`

```sql
SELECT id FROM episodes WHERE status = 'open' AND session_ids LIKE '%<sessionID>%' LIMIT 1
```

- Returns `("", false, nil)` when `sql.ErrNoRows`
- Returns `(id, true, nil)` on match
- Returns `("", false, err)` on any other error

#### `AppendEvent(episodeID string, ev Event) error`

- If `ev.ID == ""`: set `ev.ID = uuid.New().String()`
- If `ev.Timestamp == 0`: set `ev.Timestamp = time.Now().UnixMilli()`
- Serialise `ev.Payload` to JSON (`json.Marshal`)
- Store empty string `""` for `parent_id` when `ev.ParentID == ""` — use a helper that returns `nil` for SQL NULL on empty strings
- INSERT the event row

#### `CloseEpisode(episodeID, outcome string) error`

1. Call `extractSummary(episodeID)` to build a keyword summary from `llm_response` payloads (see below)
2. `UPDATE episodes SET status = 'closed', closed_at = ?, outcome = ?, summary = ? WHERE id = ?`
3. Call `AppendEvent(episodeID, Event{Type: TypeEpisodeClose, Payload: map[string]any{"outcome": outcome, "summary": summary}})`

#### `extractSummary(episodeID string) string` (unexported)

- `SELECT payload FROM events WHERE episode_id = ? AND type = 'llm_response' ORDER BY timestamp ASC`
- For each row: unmarshal payload JSON; extract `payload["content"].(string)`; truncate to 100 characters
- Join up to all snippets with ` | ` separator
- Return empty string on any error

#### `QueryByTime(from, to time.Time) ([]Episode, error)`

```sql
SELECT id, title, status, created_at, closed_at, session_ids, tags, outcome, summary
  FROM episodes
 WHERE created_at >= ? AND created_at <= ?
 ORDER BY created_at ASC
```

Arguments: `from.Unix()`, `to.Unix()`.

#### `QueryByFilter(status, outcome string) ([]Episode, error)`

Build the query dynamically:

```sql
SELECT id, title, status, created_at, closed_at, session_ids, tags, outcome, summary
  FROM episodes
 WHERE 1=1
   [AND status = ?]     -- only when status != ""
   [AND outcome = ?]    -- only when outcome != ""
 ORDER BY created_at DESC
```

#### `StartTimeoutWorker(ctx context.Context, timeout time.Duration)`

- If `timeout <= 0`: return immediately (disabled)
- `interval := max(timeout/2, time.Minute)` (Go 1.21+ builtin `max`)
- Start a goroutine: ticker at `interval`; on each tick call `expireStaleEpisodes(timeout)`; stop when `ctx.Done()`

#### `expireStaleEpisodes(timeout time.Duration)` (unexported)

```sql
UPDATE episodes SET status = 'failed', outcome = 'failure'
 WHERE status = 'open' AND created_at < ?
```

Argument: `time.Now().Add(-timeout).Unix()`. Log warning on error using `slog.Default()`.

#### `Close() error`

```go
return s.db.Close()
```

---

### 2. `memory/episodic/context.go` — episode ID in context

```go
package episodic

import "context"

type contextKey int

const episodeIDKey contextKey = iota

// WithEpisodeID returns a copy of ctx carrying the episode ID.
func WithEpisodeID(ctx context.Context, episodeID string) context.Context {
    return context.WithValue(ctx, episodeIDKey, episodeID)
}

// EpisodeIDFromContext extracts the episode ID. Returns ("", false) if absent.
func EpisodeIDFromContext(ctx context.Context) (string, bool) {
    id, ok := ctx.Value(episodeIDKey).(string)
    return id, ok && id != ""
}
```

---

### 3. `orchestrator/orchestrator.go` — event hooks

Add an optional event store field and wire it into `Dispatch`.

**Add to the `Orchestrator` struct:**

```go
events *episodic.Store
```

**Add method:**

```go
func (o *Orchestrator) SetEventStore(store *episodic.Store)
```

**Rewrite `Dispatch`** to add event recording around each provider call. The episode ID is carried in the context via `episodic.EpisodeIDFromContext`. The event store may be nil (e.g. in tests); all writes must be no-ops when `o.events == nil` or when no episode ID is in the context.

Events to write per dispatch attempt (in order):

1. `TypeOrchestratorRoute` — before the provider call; payload: `{"layer": layerName, "prompt_len": len(prompt)}`
2. `TypeLLMCall` — immediately before calling `provider.Chat`; no payload required
3. `TypeLLMResponse` — on success; set `LatencyMs`, `TokensIn`, `TokensOut` from the response; payload: `{"content": resp.Content}`

On error (provider fails and fallback fires), write no `TypeLLMResponse` — just follow the fallback chain.

Add a private helper:

```go
func (o *Orchestrator) writeEvent(hasEpisode bool, episodeID string, ev episodic.Event)
```

Calls `o.events.AppendEvent(episodeID, ev)` and logs a warning on error. No-ops when `!hasEpisode || o.events == nil`.

Add a private helper:

```go
func layerNumber(name string) int
```

Returns `1`, `2`, or `3` for `"layer1"`, `"layer2"`, `"layer3"`; `0` for anything else. Used to populate `Event.Layer`.

**`DispatchStream` does not need event hooks in Phase 2** — streaming is not wired to the episodic store until Phase 4.

**New import to add to `orchestrator.go`:**

```go
"github.com/vishnuvardhansankoti/ai-nexus/memory/episodic"
```

---

### 4. `cmd/nexus/chat.go` — episodic store integration

Modify `runChat` to open the store, manage episodes, and attach the episode ID to the context passed to `Dispatch`.

**After loading config and before building the orchestrator:**

```go
dbPath := cfg.Memory.Episodic.DB
if dbPath == "" {
    dbPath = episodic.DefaultDBPath()
}
epStore, err := episodic.New(dbPath)
if err != nil {
    return fmt.Errorf("open episodic store: %w", err)
}
defer epStore.Close()

if cfg.Memory.Episodic.OpenEpisodeTimeout > 0 {
    epStore.StartTimeoutWorker(cmd.Context(), cfg.Memory.Episodic.OpenEpisodeTimeout)
}
```

**After building the orchestrator, call:**

```go
orch.SetEventStore(epStore)
```

**Episode open / resume logic** (after determining `sessionID`):

```go
var episodeID string
if chatSessionID != "" {
    // Resuming: look for an open episode for this session.
    id, found, err := epStore.FindOpenEpisode(sessionID)
    if err != nil {
        return fmt.Errorf("find episode: %w", err)
    }
    if found {
        episodeID = id
    } else {
        episodeID, err = epStore.OpenEpisode(sessionID)
        if err != nil {
            return fmt.Errorf("open episode: %w", err)
        }
    }
} else {
    episodeID, err = epStore.OpenEpisode(sessionID)
    if err != nil {
        return fmt.Errorf("open episode: %w", err)
    }
}
```

**Attach episode ID to context:**

```go
ctx := episodic.WithEpisodeID(cmd.Context(), episodeID)
```

Use this `ctx` everywhere that was previously using `cmd.Context()` (including the `orch.Dispatch` call inside `sendMessage`).

**Close episode on exit.** Add a helper closure:

```go
closeEpisode := func(outcome string) {
    if err := epStore.CloseEpisode(episodeID, outcome); err != nil {
        logger.Warn("close episode failed", "err", err)
    }
}
```

Call it in all exit paths:
- Single-shot success: `closeEpisode(episodic.OutcomeSuccess)`
- Single-shot error: `closeEpisode(episodic.OutcomeFailure)`
- REPL normal exit (`"exit"` or EOF): `closeEpisode(episodic.OutcomeSuccess)`
- REPL `Ctrl+C` / SIGINT: `closeEpisode(episodic.OutcomePartial)` (episode ends mid-session)
- REPL scanner error: `closeEpisode(episodic.OutcomeFailure)`

---

## Unit tests required

### `memory/episodic/episodic_test.go`

All tests use a temp file database (`os.CreateTemp` + `t.TempDir()`). No live Ollama or network needed.

```go
func TestOpenEpisode(t *testing.T)
// Creates episode; QueryByFilter(StatusOpen, "") returns 1 row with matching ID.

func TestAppendEvent(t *testing.T)
// Opens episode; AppendEvent with TypeLLMCall; SELECT COUNT(*) FROM events = 1.

func TestCloseEpisode(t *testing.T)
// Opens episode; appends TypeLLMResponse event with content payload;
// CloseEpisode(OutcomeSuccess); QueryByFilter(StatusClosed, OutcomeSuccess) returns 1 row;
// assert episode.Summary != "".

func TestFindOpenEpisode_Resume(t *testing.T)
// Opens episode for "sess-x"; FindOpenEpisode("sess-x") returns (id, true, nil).

func TestFindOpenEpisode_NoneAfterClose(t *testing.T)
// Opens + closes episode; FindOpenEpisode returns ("", false, nil).

func TestQueryByTime(t *testing.T)
// Opens episode; QueryByTime(before, after) returns 1 episode with correct ID.

func TestQueryByFilter_StatusAndOutcome(t *testing.T)
// Opens 2 episodes; closes one as success, one as failure;
// QueryByFilter(StatusClosed, "") returns 2; QueryByFilter(StatusClosed, OutcomeSuccess) returns 1.

func TestTimeoutWorker_MarksStaleEpisodes(t *testing.T)
// Opens episode; backdates created_at to 2 hours ago via direct SQL;
// calls expireStaleEpisodes(1h); QueryByFilter(StatusFailed, "") returns 1 row.

func TestContextEpisodeID(t *testing.T)
// Bare context: EpisodeIDFromContext returns ("", false).
// WithEpisodeID → EpisodeIDFromContext returns ("ep-abc", true).
```

---

## Package import rules

These extend the Phase 0/1 rules to cover the new episodic imports:

- `cmd` → anything
- `orchestrator` → `providers/*`, `config`, `sdk`, `memory/episodic`
- `memory/episodic` → `config`, `sdk` (do NOT import `orchestrator`)
- `memory/working` → `config`, `sdk`
- `providers/*` → `config`, `sdk`
- `sdk` → nothing

---

## Acceptance criteria ("done when")

Phase 2 is complete when ALL of the following pass:

1. `go build ./...` exits 0
2. `make build` produces `bin/nexus`
3. `go test ./...` exits 0 — all episodic tests pass
4. `go vet ./...` exits 0
5. All 9 episodic unit tests pass without a live Ollama instance
6. `nexus chat --prompt "what is 2+2"` writes an episode row (status=closed, outcome=success) and at least 3 events to `~/.nexus/episodic.db`
7. Kill `nexus chat` with Ctrl+C mid-session; the episode row has `status='open'` (or `status='partial'` if you close on signal); restart with `nexus chat --session <id>` and confirm events continue in the same episode row
8. Open the SQLite DB manually (`sqlite3 ~/.nexus/episodic.db`) and verify: `SELECT type, layer, latency_ms, tokens_in, tokens_out FROM events;` shows `orchestrator_route`, `llm_call`, and `llm_response` rows with non-zero values for a completed session

---

## Git and PR instructions

1. `git checkout main && git pull origin main`
2. `git checkout -b phase/2-episodic-store`
3. Implement all deliverables
4. Run all acceptance criteria checks
5. `git add` only the files you created/modified (not `docs/`)
6. `git commit -m "feat: Phase 2 — episodic store"`
7. `git push origin phase/2-episodic-store`
8. Open PR to `main` with title `Phase 2 — Episodic Store` and body listing acceptance criteria as checkboxes

---

## What NOT to do

- Do not implement Phase 3 (governance middleware), Phase 4 (REST API), or Phase 5 (observability) — those stubs remain unchanged
- Do not add semantic or archival memory logic — `memory/semantic` and `memory/archival` remain stubs
- Do not add LLM-based summary extraction — use the keyword stub only (v2 will replace it)
- Do not populate the `embedding` column — leave it NULL (v2 adds the embedding model call)
- Do not add any new Go module dependencies — all required packages are already in `go.mod`
- Do not modify `sdk/interfaces.go`
- Do not commit to `main` directly
- Do not use `github.com/mattn/go-sqlite3` (CGo) — `modernc.org/sqlite` only
