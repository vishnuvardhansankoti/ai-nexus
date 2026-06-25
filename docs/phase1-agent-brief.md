# Phase 1 Agent Brief — 3-Layer Routing + CLI

## Context

You are implementing Phase 1 of Nexus, an AI agent harness written in Go. Work in the git repository at the current working directory. Phase 0 is already merged to `main` — all packages exist as stubs, `sdk/interfaces.go` defines the canonical interfaces, and `config/config.go` has the full config struct. Your job is to implement the four sub-phases of Phase 1 on branch `phase/1-routing-cli` and open a PR to `main`.

**Read these files before starting:**
- `sdk/interfaces.go` — all interfaces you must implement
- `config/config.go` — the full Config struct you must read from
- `go.mod` — current dependencies

## Locked decisions

| Decision | Value |
|---|---|
| Module path | `github.com/vishnuvardhansankoti/ai-nexus` |
| Branch | `phase/1-routing-cli` |
| PR target | `main` |
| SQLite driver | `modernc.org/sqlite` (pure Go, no CGo — CI runs Linux) |
| CLI framework | `github.com/spf13/cobra` |

## New dependencies to add

```
github.com/spf13/cobra
github.com/google/uuid
modernc.org/sqlite
github.com/anthropics/anthropic-sdk-go
google.golang.org/genai
github.com/openai/openai-go
```

Run `go get` for each, then `go mod tidy`.

---

## Sub-phase 1a — Providers

### Ollama provider: `providers/ollama/ollama.go`

Replace the stub. Implement `sdk.Provider` against the Ollama `/api/chat` endpoint.

```go
type Client struct {
    baseURL    string
    model      string
    httpClient *http.Client // timeout set from layer config
}

func New(baseURL, model string, timeout time.Duration) *Client
```

**`Chat(ctx, req) (sdk.Response, error)`**
- POST to `<baseURL>/api/chat` with body:
  ```json
  {"model": "<model>", "messages": [...], "stream": false}
  ```
- Ollama message format: `[{"role": "user", "content": "..."}]` — map `sdk.Message` directly
- Parse response: `{"message": {"role": "assistant", "content": "..."}, "done": true, "prompt_eval_count": N, "eval_count": N}`
- Return `sdk.Response{Content: ..., Model: ..., TokensIn: prompt_eval_count, TokensOut: eval_count}`
- On HTTP error or timeout: return wrapped error

**`ChatStream(ctx, req, out chan<- string) error`**
- POST to `/api/chat` with `"stream": true`
- Ollama streams newline-delimited JSON (NDJSON). Each line:
  ```json
  {"message": {"role": "assistant", "content": "<token>"}, "done": false}
  ```
  Final line has `"done": true`.
- Send each content token to `out` channel as it arrives
- Close `out` when done or on error
- Return error if any

**`Ping(ctx) error`**
- GET `<baseURL>/api/tags` — returns 200 if Ollama is up
- Return nil on 200, error otherwise

**Pre-warm helper** (called by orchestrator at startup):
```go
func (c *Client) Warmup(ctx context.Context) error
```
- Send a minimal Chat request: `messages: [{role: "user", content: "hi"}]`
- If response arrives within ctx deadline: return nil (layer is available)
- If timeout or error: return error (layer marked unavailable)

---

### Cloud providers

Create sub-packages. Replace `providers/cloud/cloud.go` with:

```
providers/cloud/anthropic/anthropic.go
providers/cloud/google/google.go
providers/cloud/openai/openai.go
```

Each implements `sdk.Provider`. Use the official SDKs added as dependencies above.

**`providers/cloud/anthropic/anthropic.go`**
- Use `github.com/anthropics/anthropic-sdk-go`
- Constructor: `New(model, apiKey string, timeout time.Duration) *Client`
- `Chat`: call `client.Messages.New(ctx, ...)` — map `sdk.Request.Messages` to Anthropic message params; extract text from response content blocks
- `ChatStream`: use streaming messages API; send text delta tokens to `out` channel
- `Ping`: call `client.Models.Get(ctx, model)` — confirms key is valid and model exists; return error if not
- API key: use the passed `apiKey`; also check `NEXUS_LAYER3_API_KEY` env var as fallback

**`providers/cloud/google/google.go`**
- Use `google.golang.org/genai`
- Constructor: `New(model, apiKey string, timeout time.Duration) *Client`
- `Chat`: `genai.GenerateContent` with mapped messages
- `ChatStream`: `genai.GenerateContentStream` — send tokens to `out`
- `Ping`: list models or call a lightweight endpoint to validate key

**`providers/cloud/openai/openai.go`**
- Use `github.com/openai/openai-go`
- Constructor: `New(model, apiKey, baseURL string, timeout time.Duration) *Client`
- `baseURL` allows pointing at any OpenAI-compatible endpoint (e.g. local vLLM)
- `Chat`: `client.Chat.Completions.New(ctx, ...)` — map messages; extract content from first choice
- `ChatStream`: streaming completions; send delta content tokens to `out`
- `Ping`: `client.Models.Get(ctx, model)` — validates key and model

---

### Provider factory: `providers/factory.go`

```go
package providers

import "github.com/vishnuvardhansankoti/ai-nexus/sdk"

// New constructs the correct sdk.Provider for a layer config.
// provider field: "ollama" | "anthropic" | "google" | "openai"
func New(cfg config.LayerConfig) (sdk.Provider, error)
```

- Reads `cfg.Provider` and constructs the matching implementation
- For `"ollama"`: returns `ollama.New(cfg.BaseURL, cfg.Model, cfg.Timeout)`
- For `"anthropic"`: returns `anthropic.New(cfg.Model, cfg.APIKey, cfg.Timeout)`
- For `"google"`: returns `google.New(cfg.Model, cfg.APIKey, cfg.Timeout)`
- For `"openai"`: returns `openai.New(cfg.Model, cfg.APIKey, cfg.BaseURL, cfg.Timeout)`
- Unknown provider: return `fmt.Errorf("unknown provider: %s", cfg.Provider)`

---

## Sub-phase 1b — Orchestrator: `orchestrator/orchestrator.go`

Replace the stub. The orchestrator classifies prompts and dispatches to the correct provider, following the fallback chain on failure.

```go
type Orchestrator struct {
    rules        []compiledRule   // compiled at startup
    defaultLayer string
    layers       map[string]sdk.Provider
    layerCfgs    map[string]config.LayerConfig
    logger       *slog.Logger
}

type compiledRule struct {
    pattern *regexp.Regexp
    layer   string
}

func New(cfg *config.Config, logger *slog.Logger) (*Orchestrator, error)
```

**Startup in `New`:**
1. For each layer in `cfg.Layers`, call `providers.New(layerCfg)` to build the provider
2. Compile each `cfg.Orchestrator.Rules[i].Pattern` to `*regexp.Regexp`; fail fast if any pattern is invalid
3. Run pre-warm concurrently for all Ollama layers: spawn a goroutine per layer, call `provider.Warmup(ctx)` with a 10s timeout; mark unavailable layers in a `map[string]bool`; log a structured warning for each unavailable layer: `slog.Warn("layer unavailable", "layer", name, "reason", err.Error())`
4. Return the constructed orchestrator

**`Classify(prompt string) string`** — returns the layer name
- Iterate `rules` in order; return first matching rule's layer
- If no rule matches: return `cfg.Orchestrator.DefaultLayer`

**`Dispatch(ctx context.Context, prompt string, req sdk.Request) (sdk.Response, error)`**
- Call `Classify(prompt)` to get starting layer name
- Follow the fallback chain:
  ```
  layer = startLayer
  for layer != "" {
      if layer is available:
          resp, err = layers[layer].Chat(ctx, req)
          if err == nil: return resp, nil
          log fallback warning: {"event":"fallback","from":layer,"to":next,"reason":err}
      layer = layerCfgs[layer].FallbackTo
  }
  return error: "all layers exhausted"
  ```
- Structured fallback log: `slog.Warn("fallback", "event", "fallback", "from", currentLayer, "to", nextLayer, "reason", err.Error())`

**`DispatchStream(ctx, prompt string, req sdk.Request, out chan<- string) error`**
- Same fallback logic but calls `provider.ChatStream` instead

**`Status() map[string]bool`** — returns layer name → available

---

## Sub-phase 1c — Working Context: `memory/working/working.go`

Replace the stub. Two responsibilities: in-process message buffer + SQLite session file.

```go
type Buffer struct {
    Messages []sdk.Message
}

func (b *Buffer) Append(msg sdk.Message)
func (b *Buffer) All() []sdk.Message
func (b *Buffer) Clear()
```

```go
type SessionStore struct {
    db        *sql.DB
    sessionID string
}

func NewSessionStore(sessionID string) (*SessionStore, error)
// Opens/creates ~/.nexus/sessions/<sessionID>.db
// Creates table if not exists:
//   CREATE TABLE IF NOT EXISTS messages (
//     id INTEGER PRIMARY KEY AUTOINCREMENT,
//     role TEXT NOT NULL,
//     content TEXT NOT NULL,
//     created_at INTEGER NOT NULL
//   )

func (s *SessionStore) Append(msg sdk.Message) error
// INSERT INTO messages (role, content, created_at) VALUES (?, ?, unixepoch())

func (s *SessionStore) Load() ([]sdk.Message, error)
// SELECT role, content FROM messages ORDER BY id ASC

func (s *SessionStore) Close() error
```

Use `modernc.org/sqlite` as the driver (`import _ "modernc.org/sqlite"`), registered under the driver name `"sqlite"`. Use `sql.Open("sqlite", path)`.

Session DB path: `~/.nexus/sessions/<sessionID>.db`. Create `~/.nexus/sessions/` if it doesn't exist.

---

## Sub-phase 1d — CLI: `cmd/nexus/main.go`

Replace the current stub with a full cobra CLI. Structure all commands in `cmd/nexus/` — you may create additional files (`chat.go`, `status.go`, `config_cmd.go`) within the same `package main`.

### Root command

```go
var rootCmd = &cobra.Command{
    Use:   "nexus",
    Short: "AI agent harness with 3-layer routing",
}

func main() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

`Version` and `Commit` vars remain; wire them into `nexus version`.

### `nexus chat`

```go
var chatCmd = &cobra.Command{
    Use:   "chat",
    Short: "Start an interactive chat session",
    RunE:  runChat,
}
```

Flags:
- `--prompt` / `-p` string — single-shot mode; send this prompt and exit
- `--session` / `-s` string — resume session by ID; if omitted, generate new UUID

**Interactive REPL (no `--prompt` flag):**
1. Load config from `config.DefaultConfigPath()`
2. Create orchestrator
3. Create/resume session: if `--session` given, call `sessionStore.Load()` and populate buffer; else generate `uuid.New().String()` as session ID
4. Print: `nexus session <sessionID>`
5. Loop: `fmt.Print("> ")` → read line from stdin → if "exit" or Ctrl+C: close → send to orchestrator.Dispatch → print response → append both user message and assistant response to buffer and session store
6. On Ctrl+C / "exit": print newline and exit cleanly

**Single-shot (`--prompt` given):**
1. Same setup (config, orchestrator, session)
2. Dispatch the prompt once
3. Print response to stdout
4. Append to session store
5. Exit

### `nexus status`

Print a table of layer health. Call `orchestrator.Status()` and for each layer ping the provider:

```
LAYER    PROVIDER    MODEL              STATUS    LATENCY
layer1   ollama      llama3.2:3b        up        42ms
layer2   ollama      llama3.1:8b        up        51ms
layer3   anthropic   claude-sonnet-4-6  up        -
```

Use `text/tabwriter` for alignment. Latency is from the Ping call duration; show `-` for cloud providers (no warm-up inference).

### `nexus config validate`

```go
_, err := config.Load(config.DefaultConfigPath())
if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
fmt.Println("OK")
```

### `nexus config show`

Load config, marshal to JSON with `encoding/json`, pretty-print to stdout.

### `nexus version`

```go
fmt.Printf("nexus %s (%s)\n", Version, Commit)
```

### `nexus serve` (stub only in Phase 1)

```go
var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "Start the REST API server (implemented in Phase 4)",
    RunE: func(cmd *cobra.Command, args []string) error {
        return fmt.Errorf("REST API server not yet implemented (Phase 4)")
    },
}
```

---

## Unit tests required

Every sub-phase needs at least one unit test before the phase is marked done.

### `providers/ollama/ollama_test.go`

Use `net/http/httptest` to mock the Ollama server.

```go
func TestChat_success(t *testing.T) // mock server returns valid NDJSON; assert response content
func TestChat_timeout(t *testing.T) // mock server delays > timeout; assert error
func TestPing_up(t *testing.T)      // mock /api/tags returns 200; assert nil error
func TestPing_down(t *testing.T)    // mock returns 503; assert error
```

### `orchestrator/orchestrator_test.go`

Use a mock `sdk.Provider` (implement the interface inline with a struct).

```go
func TestClassify_matchesFirstRule(t *testing.T)  // "what is Go" → layer1
func TestClassify_defaultLayer(t *testing.T)      // "unmatched prompt" → layer2 (default)
func TestDispatch_fallback(t *testing.T)          // layer1 provider errors; assert layer2 called
func TestDispatch_allLayersExhausted(t *testing.T)
```

### `memory/working/working_test.go`

```go
func TestSessionStore_appendAndLoad(t *testing.T) // write 2 messages, load, assert order
func TestBuffer_appendAndClear(t *testing.T)
```

### `config` (add to existing `config_test.go`)

```go
func TestDefaultConfigPath(t *testing.T) // already exists; keep it
```

---

## Package import rules (from Phase 0 — enforce these)

- `cmd` → anything
- `orchestrator` → `providers/*`, `config`, `sdk`
- `memory/*` → `config`, `sdk`
- `providers/*` → `config`, `sdk`
- `sdk` → nothing

---

## Acceptance criteria ("done when")

Phase 1 is complete when ALL of the following pass:

1. `go build ./...` exits 0
2. `make build` produces `bin/nexus`
3. `go test ./...` exits 0 — all unit tests pass (mock-based, no live Ollama needed)
4. `go vet ./...` exits 0
5. `./bin/nexus version` prints `nexus v0.0.0-dev (<commit>)`
6. `./bin/nexus config validate` with a valid config YAML prints `OK`
7. `./bin/nexus config show` with a valid config YAML prints JSON
8. Orchestrator unit tests: keyword classification routes correctly; fallback chain fires when provider errors
9. Ollama provider unit tests: mock server returns correct response; timeout returns error
10. Session store unit test: append + load round-trips correctly

Verify each criterion before committing. You do NOT need a live Ollama instance for any of these.

---

## Git and PR instructions

1. `git checkout main && git pull origin main`
2. `git checkout -b phase/1-routing-cli`
3. Implement all sub-phases
4. Run all acceptance criteria checks
5. `git add` only the files you created/modified (not docs/)
6. `git commit -m "feat: Phase 1 — 3-layer routing + CLI"`
7. `git push origin phase/1-routing-cli`
8. Open PR to `main` with title `Phase 1 — 3-Layer Routing + CLI` and body listing acceptance criteria as checkboxes

---

## What NOT to do

- Do not implement Phase 2 (episodic store), Phase 3 (governance), or Phase 4 (REST API) — stubs from Phase 0 remain as-is
- Do not modify `sdk/interfaces.go` — implement the existing interfaces as-is
- Do not add dependencies beyond those listed in this brief
- Do not use `github.com/mattn/go-sqlite3` (CGo) — use `modernc.org/sqlite` only
- Do not write a live integration test that requires Ollama — all tests must pass without an Ollama instance
