# Phase 0 Agent Brief — Repository Scaffold

## Context

You are implementing Phase 0 of Nexus, an AI agent harness written in Go. You are working in the existing git repository at the current working directory. The repository currently contains only a `docs/` folder with planning documents. Your job is to produce the full Phase 0 scaffold as described below, commit it to a new branch `phase/0-scaffold`, and open a GitHub PR targeting `main`.

## Locked decisions

| Decision | Value |
|---|---|
| Module path | `github.com/vishnuvardhansankoti/ai-nexus` |
| Go version | 1.24 |
| Branch | `phase/0-scaffold` |
| PR target | `main` |

## What to build

### 1. `go.mod`

```
module github.com/vishnuvardhansankoti/ai-nexus

go 1.24
```

Run `go mod tidy` after creating all files.

### 2. Directory skeleton

Create each package as an empty Go package with a single `.go` file containing only the `package` declaration and a brief package doc comment. Do not put any logic in stub files.

```
cmd/nexus/main.go          package main
api/api.go                 package api
orchestrator/orchestrator.go  package orchestrator
governance/governance.go   package governance
memory/working/working.go  package working
memory/episodic/episodic.go package episodic
memory/semantic/semantic.go package semantic
memory/archival/archival.go package archival
providers/ollama/ollama.go  package ollama
providers/cloud/cloud.go   package cloud
config/config.go           package config
registry/registry.go       package registry
agents/coding/coding.go    package coding
sdk/sdk.go                 package sdk
```

`cmd/nexus/main.go` must have a real `main()` function (even if it just prints a version line) so that `go build ./cmd/nexus` produces a binary.

### 3. `sdk/interfaces.go` — versioned interfaces

All cross-package communication goes through these interfaces. No concrete types cross package boundaries.

```go
package sdk

import "context"

// Request is the normalised input sent to any provider or agent.
type Request struct {
	Messages []Message
	Model    string
	Stream   bool
}

// Message is a single turn in a conversation.
type Message struct {
	Role    string // "user" | "assistant" | "system"
	Content string
}

// Response is the normalised output from any provider or agent.
type Response struct {
	Content  string
	Model    string
	TokensIn int
	TokensOut int
}

// Provider is implemented by every LLM backend (Ollama, Anthropic, Google, OpenAI).
type Provider interface {
	Chat(ctx context.Context, req Request) (Response, error)
	ChatStream(ctx context.Context, req Request, out chan<- string) error
	Ping(ctx context.Context) error
}

// Agent is implemented by every sub-agent (coding agent, etc.).
type Agent interface {
	Name() string
	Version() string
	Run(ctx context.Context, req Request) (Response, error)
}

// Tool is implemented by every discrete tool (web search, file read, etc.).
type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, input map[string]any) (map[string]any, error)
}

// Skill is a composable multi-step pipeline of tools and agent calls.
type Skill interface {
	Name() string
	Run(ctx context.Context, input string) (string, error)
}

// MemoryStore is the common interface for all memory tiers.
type MemoryStore interface {
	Write(ctx context.Context, key string, value []byte) error
	Query(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}
```

### 4. `config/config.go` — YAML loader + validation

Implement a `Load(path string) (*Config, error)` function using `gopkg.in/yaml.v3`. Validate the loaded struct using `github.com/go-playground/validator/v10`. The `Config` struct must mirror the full v1 configuration schema below exactly — every field, every nested struct.

**Full v1 config schema to mirror:**

```yaml
server:
  port: 11434
  local_dev: false

auth:
  api_keys:
    - key: "nexus-key-abc123"
      name: "default"
      rate_limit_rpm: 60

layers:
  layer1:
    provider: ollama          # ollama | anthropic | google | openai
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
    api_key: ""               # used when provider != ollama
    fallback_to: null

orchestrator:
  strategy: keyword
  rules:
    - pattern: "(what is|define|explain briefly)"
      layer: layer1
    - pattern: "(write|implement|refactor|debug)"
      layer: layer2
    - pattern: "(architect|design|analyse entire|compare at length)"
      layer: layer3
  default_layer: layer2

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
    block_session_tokens: 0

memory:
  working:
    ttl: 30m
  episodic:
    db: ~/.nexus/episodes.db
    open_episode_timeout: 72h
    extraction_strategy: keyword
    vector_index: sqlite-vec
  semantic:
    enabled: false
  archival:
    enabled: false

logging:
  level: info
  format: json
  output: stdout

metrics:
  enabled: true
  path: /metrics
```

Use `time.Duration` for timeout fields. Use `validate` struct tags for required fields. Expose a `DefaultConfigPath() string` helper that returns `~/.nexus/config.yaml` expanded.

### 5. `Makefile`

```makefile
BINARY_NAME=nexus
MODULE=github.com/vishnuvardhansankoti/ai-nexus
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION=v0.0.0-dev

.PHONY: build test lint generate clean

build:
	go build -ldflags "-X $(MODULE)/cmd/nexus.Version=$(VERSION) -X $(MODULE)/cmd/nexus.Commit=$(COMMIT)" -o bin/$(BINARY_NAME) ./cmd/nexus

test:
	go test ./...

lint:
	golangci-lint run ./...

generate:
	go generate ./...

clean:
	rm -rf bin/
```

### 6. `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  build-and-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"

      - name: Build
        run: go build ./...

      - name: Test
        run: go test ./...

      - name: Lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

### 7. `openapi.yaml` skeleton

```yaml
openapi: "3.1.0"
info:
  title: Nexus API
  version: "0.1.0"
  description: OpenAI-compatible AI routing layer

paths:
  /v1/chat/completions:
    post:
      summary: Create a chat completion
      operationId: createChatCompletion
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/ChatCompletionRequest"
      responses:
        "200":
          description: Successful completion
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ChatCompletionResponse"
        "401":
          description: Unauthorized
        "429":
          description: Rate limit exceeded
      security:
        - BearerAuth: []

  /healthz:
    get:
      summary: Health check
      operationId: healthz
      responses:
        "200":
          description: OK

  /metrics:
    get:
      summary: Prometheus metrics
      operationId: metrics
      responses:
        "200":
          description: Prometheus text format

  /v1/models:
    get:
      summary: List available models
      operationId: listModels
      responses:
        "200":
          description: Model list
      security:
        - BearerAuth: []

components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer

  schemas:
    ChatCompletionRequest:
      type: object
      required: [model, messages]
      properties:
        model:
          type: string
        messages:
          type: array
          items:
            $ref: "#/components/schemas/Message"
        stream:
          type: boolean
          default: false

    Message:
      type: object
      required: [role, content]
      properties:
        role:
          type: string
          enum: [system, user, assistant]
        content:
          type: string

    ChatCompletionResponse:
      type: object
      properties:
        id:
          type: string
        object:
          type: string
        choices:
          type: array
          items:
            $ref: "#/components/schemas/Choice"
        usage:
          $ref: "#/components/schemas/Usage"

    Choice:
      type: object
      properties:
        index:
          type: integer
        message:
          $ref: "#/components/schemas/Message"
        finish_reason:
          type: string

    Usage:
      type: object
      properties:
        prompt_tokens:
          type: integer
        completion_tokens:
          type: integer
        total_tokens:
          type: integer
```

## Package import rules

Enforce these in code — no circular imports:

- `cmd` → anything
- `api` → `orchestrator`, `governance`, `memory/*`, `providers/*`, `config`, `sdk`
- `orchestrator` → `providers/*`, `config`, `sdk`
- `governance` → `config`, `sdk`
- `memory/*` → `config`, `sdk`
- `providers/*` → `config`, `sdk`
- `sdk` → nothing (no internal imports)

## Go module dependencies to add

Run `go get` for these after creating all files:

```
gopkg.in/yaml.v3
github.com/go-playground/validator/v10
```

Do not add any other dependencies in Phase 0. All other packages (cobra, chi, sqlite3, prometheus, etc.) are added in their respective phases.

## Acceptance criteria ("done when")

Phase 0 is complete when ALL of the following pass:

1. `go build ./...` exits 0 with no errors
2. `make build` produces `bin/nexus`
3. `./bin/nexus` exits without panic
4. `go test ./...` exits 0 (no test files yet — this just confirms compilation)
5. `go vet ./...` exits 0
6. `config.Load("nonexistent.yaml")` returns an error (not a panic) — verify with a minimal test in `config/config_test.go`

Verify each acceptance criterion before committing.

## Git and PR instructions

1. Create and switch to branch `phase/0-scaffold`
2. Stage only the files you created (do not stage anything in `docs/`)
3. Commit with message: `feat: Phase 0 — repository scaffold`
4. Push to `origin phase/0-scaffold`
5. Open a PR targeting `main` with title `Phase 0 — Repository Scaffold` and body listing the acceptance criteria with checkboxes

## What NOT to do

- Do not implement any business logic — stubs only for all packages except `sdk` and `config`
- Do not add dependencies beyond `yaml.v3` and `validator/v10`
- Do not modify anything in the `docs/` folder
- Do not commit to `main` directly
