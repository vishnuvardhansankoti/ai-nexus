// Package ollama provides an sdk.Provider implementation for the Ollama local model server.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// Client is an Ollama HTTP client implementing sdk.Provider.
type Client struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// New creates an Ollama client for the given base URL, model, and per-request timeout.
func New(baseURL, model string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type chatResponse struct {
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

// Chat sends a non-streaming request to Ollama and returns the assembled response.
func (c *Client) Chat(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	msgs := make([]ollamaMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = ollamaMessage{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(chatRequest{Model: c.model, Messages: msgs, Stream: false})
	if err != nil {
		return sdk.Response{}, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return sdk.Response{}, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return sdk.Response{}, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return sdk.Response{}, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return sdk.Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}

	return sdk.Response{
		Content:   chatResp.Message.Content,
		Model:     c.model,
		TokensIn:  chatResp.PromptEvalCount,
		TokensOut: chatResp.EvalCount,
	}, nil
}

// ChatStream sends a streaming request to Ollama, writing content tokens to out.
// Closes out when done or on error.
func (c *Client) ChatStream(ctx context.Context, req sdk.Request, out chan<- string) error {
	defer close(out)

	msgs := make([]ollamaMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = ollamaMessage{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(chatRequest{Model: c.model, Messages: msgs, Stream: true})
	if err != nil {
		return fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// No hard timeout on the streaming client — context cancellation controls it.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama: stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var chunk chatResponse
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			return fmt.Errorf("ollama: decode stream chunk: %w", err)
		}
		if chunk.Message.Content != "" {
			select {
			case out <- chunk.Message.Content:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if chunk.Done {
			break
		}
	}
	return scanner.Err()
}

// Ping checks whether the Ollama server is reachable via GET /api/tags.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("ollama: build ping request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: ping failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: ping returned status %d", resp.StatusCode)
	}
	return nil
}

// Warmup sends a minimal chat request to verify the model is loaded.
// Returns nil if the model responds within ctx's deadline.
func (c *Client) Warmup(ctx context.Context) error {
	_, err := c.Chat(ctx, sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "hi"}},
		Model:    c.model,
	})
	return err
}

// WarmupTimeout is the default timeout used for pre-warm requests.
const WarmupTimeout = 10 * time.Second
