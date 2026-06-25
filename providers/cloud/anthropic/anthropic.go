// Package anthropic provides an sdk.Provider implementation for the Anthropic Claude API.
package anthropic

import (
	"context"
	"fmt"
	"os"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// Client implements sdk.Provider for the Anthropic Messages API.
type Client struct {
	client  anthropicsdk.Client
	model   string
	timeout time.Duration
}

// New creates an Anthropic client. If apiKey is empty, NEXUS_LAYER3_API_KEY is used.
func New(model, apiKey string, timeout time.Duration) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("NEXUS_LAYER3_API_KEY")
	}
	c := anthropicsdk.NewClient(option.WithAPIKey(apiKey))
	return &Client{client: c, model: model, timeout: timeout}
}

func toAnthropicMessages(msgs []sdk.Message) []anthropicsdk.MessageParam {
	out := make([]anthropicsdk.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(m.Content)))
		case "assistant":
			out = append(out, anthropicsdk.NewAssistantMessage(anthropicsdk.NewTextBlock(m.Content)))
		}
	}
	return out
}

// Chat sends a non-streaming request to the Anthropic Messages API.
func (c *Client) Chat(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Messages.New(ctx, anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(c.model),
		MaxTokens: 4096,
		Messages:  toAnthropicMessages(req.Messages),
	})
	if err != nil {
		return sdk.Response{}, fmt.Errorf("anthropic: chat failed: %w", err)
	}

	var content string
	for _, block := range resp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return sdk.Response{
		Content:   content,
		Model:     c.model,
		TokensIn:  int(resp.Usage.InputTokens),
		TokensOut: int(resp.Usage.OutputTokens),
	}, nil
}

// ChatStream sends the response by calling Chat and forwarding the full content as one token.
// True token-level streaming can be layered on later without changing the interface.
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

// Ping validates that the configured model is accessible with the current API key.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := c.client.Models.Get(ctx, c.model, anthropicsdk.ModelGetParams{})
	if err != nil {
		return fmt.Errorf("anthropic: ping failed: %w", err)
	}
	return nil
}
