// Package openai provides an sdk.Provider implementation for the OpenAI Chat Completions API.
// It also works with any OpenAI-compatible endpoint (e.g. local vLLM).
package openai

import (
	"context"
	"fmt"
	"os"
	"time"

	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// Client implements sdk.Provider for the OpenAI Chat Completions API.
type Client struct {
	client  openaisdk.Client
	model   string
	timeout time.Duration
}

// New creates an OpenAI client. If apiKey is empty, NEXUS_LAYER3_API_KEY is used.
// baseURL overrides the default OpenAI endpoint for OpenAI-compatible servers.
func New(model, apiKey, baseURL string, timeout time.Duration) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("NEXUS_LAYER3_API_KEY")
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	c := openaisdk.NewClient(opts...)
	return &Client{client: c, model: model, timeout: timeout}
}

func toOpenAIMessages(msgs []sdk.Message) []openaisdk.ChatCompletionMessageParamUnion {
	out := make([]openaisdk.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, openaisdk.UserMessage(m.Content))
		case "assistant":
			out = append(out, openaisdk.AssistantMessage(m.Content))
		case "system":
			out = append(out, openaisdk.SystemMessage(m.Content))
		}
	}
	return out
}

// Chat sends a non-streaming request to the OpenAI Chat Completions API.
func (c *Client) Chat(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Chat.Completions.New(ctx, openaisdk.ChatCompletionNewParams{
		Model:    openaisdk.ChatModel(c.model),
		Messages: toOpenAIMessages(req.Messages),
	})
	if err != nil {
		return sdk.Response{}, fmt.Errorf("openai: chat failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return sdk.Response{}, fmt.Errorf("openai: empty choices in response")
	}

	return sdk.Response{
		Content:   resp.Choices[0].Message.Content,
		Model:     c.model,
		TokensIn:  int(resp.Usage.PromptTokens),
		TokensOut: int(resp.Usage.CompletionTokens),
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

// Ping validates that the configured model is accessible with the current API key.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := c.client.Models.Get(ctx, c.model)
	if err != nil {
		return fmt.Errorf("openai: ping failed: %w", err)
	}
	return nil
}
