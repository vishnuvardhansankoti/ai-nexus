// Package providers contains the provider factory and cloud sub-packages.
package providers

import (
	"fmt"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
	"github.com/vishnuvardhansankoti/ai-nexus/providers/cloud/anthropic"
	"github.com/vishnuvardhansankoti/ai-nexus/providers/cloud/google"
	"github.com/vishnuvardhansankoti/ai-nexus/providers/cloud/openai"
	"github.com/vishnuvardhansankoti/ai-nexus/providers/ollama"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// New constructs the correct sdk.Provider for a layer config.
// Supported provider values: "ollama", "anthropic", "google", "openai".
func New(cfg config.LayerConfig) (sdk.Provider, error) {
	switch cfg.Provider {
	case "ollama":
		return ollama.New(cfg.BaseURL, cfg.Model, cfg.Timeout), nil
	case "anthropic":
		return anthropic.New(cfg.Model, cfg.APIKey, cfg.Timeout), nil
	case "google":
		return google.New(cfg.Model, cfg.APIKey, cfg.Timeout), nil
	case "openai":
		return openai.New(cfg.Model, cfg.APIKey, cfg.BaseURL, cfg.Timeout), nil
	default:
		return nil, fmt.Errorf("providers: unknown provider %q", cfg.Provider)
	}
}
