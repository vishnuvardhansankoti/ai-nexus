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
	Content   string
	Model     string
	TokensIn  int
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
