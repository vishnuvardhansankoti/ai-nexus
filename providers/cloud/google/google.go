// Package google provides an sdk.Provider implementation for the Google Gemini API.
package google

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/genai"

	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// Client implements sdk.Provider for the Google Gemini API.
type Client struct {
	apiKey  string
	model   string
	timeout time.Duration
}

// New creates a Google Gemini client. If apiKey is empty, NEXUS_LAYER3_API_KEY is used.
func New(model, apiKey string, timeout time.Duration) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("NEXUS_LAYER3_API_KEY")
	}
	return &Client{apiKey: apiKey, model: model, timeout: timeout}
}

func (c *Client) newGenAIClient(ctx context.Context) (*genai.Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  c.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("google: create client: %w", err)
	}
	return client, nil
}

func toGenAIContents(messages []sdk.Message) []*genai.Content {
	contents := make([]*genai.Content, 0, len(messages))
	for _, m := range messages {
		role := genai.Role(m.Role)
		if m.Role == "assistant" {
			role = genai.Role("model")
		}
		contents = append(contents, genai.NewContentFromText(m.Content, role))
	}
	return contents
}

// Chat sends a non-streaming request to the Gemini API.
func (c *Client) Chat(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	gc, err := c.newGenAIClient(ctx)
	if err != nil {
		return sdk.Response{}, err
	}

	resp, err := gc.Models.GenerateContent(ctx, c.model, toGenAIContents(req.Messages), nil)
	if err != nil {
		return sdk.Response{}, fmt.Errorf("google: generate content failed: %w", err)
	}

	return sdk.Response{
		Content:   resp.Text(),
		Model:     c.model,
		TokensIn:  int(resp.UsageMetadata.PromptTokenCount),
		TokensOut: int(resp.UsageMetadata.CandidatesTokenCount),
	}, nil
}

// ChatStream sends the response by calling Chat and forwarding the full content as one token.
func (c *Client) ChatStream(ctx context.Context, req sdk.Request, out chan<- string) error {
	defer close(out)
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return err
	}
	select {
	case out <- resp.Content:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Ping validates that the Gemini API is reachable with the current API key.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gc, err := c.newGenAIClient(ctx)
	if err != nil {
		return err
	}

	_, err = gc.Models.Get(ctx, c.model, nil)
	if err != nil {
		return fmt.Errorf("google: ping failed: %w", err)
	}
	return nil
}
