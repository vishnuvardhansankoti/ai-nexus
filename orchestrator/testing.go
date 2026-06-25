package orchestrator

import (
	"log/slog"
	"regexp"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// TestOrchestrator is an alias for Orchestrator exposed for test packages.
type TestOrchestrator = Orchestrator

// NewWithMocks builds an Orchestrator with pre-injected providers, skipping the
// provider factory and warmup. All injected layers are marked available.
// If mocks is nil, Classify-only tests work without any providers.
func NewWithMocks(cfg *config.Config, mocks map[string]sdk.Provider, logger *slog.Logger) *Orchestrator {
	layerMap := map[string]config.LayerConfig{
		"layer1": cfg.Layers.Layer1,
		"layer2": cfg.Layers.Layer2,
		"layer3": cfg.Layers.Layer3,
	}

	if mocks == nil {
		mocks = make(map[string]sdk.Provider)
	}

	available := make(map[string]bool, len(layerMap))
	for name := range layerMap {
		available[name] = true
	}

	rules := make([]compiledRule, 0, len(cfg.Orchestrator.Rules))
	for _, r := range cfg.Orchestrator.Rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue
		}
		rules = append(rules, compiledRule{pattern: re, layer: r.Layer})
	}

	return &Orchestrator{
		rules:        rules,
		defaultLayer: cfg.Orchestrator.DefaultLayer,
		layers:       mocks,
		layerCfgs:    layerMap,
		available:    available,
		logger:       logger,
	}
}
