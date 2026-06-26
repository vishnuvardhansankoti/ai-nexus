// Package orchestrator classifies prompts and dispatches requests to the correct
// provider layer, following the fallback chain on failure.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
	"github.com/vishnuvardhansankoti/ai-nexus/memory/episodic"
	"github.com/vishnuvardhansankoti/ai-nexus/providers"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

type compiledRule struct {
	pattern *regexp.Regexp
	layer   string
}

// Orchestrator classifies prompts and dispatches requests to the correct layer.
type Orchestrator struct {
	rules        []compiledRule
	defaultLayer string
	layers       map[string]sdk.Provider
	layerCfgs    map[string]config.LayerConfig
	available    map[string]bool
	logger       *slog.Logger
	events       *episodic.Store
}

// SetEventStore attaches an episodic store so the orchestrator can record
// orchestrator_route, llm_call, and llm_response events.
func (o *Orchestrator) SetEventStore(store *episodic.Store) {
	o.events = store
}

// New builds an Orchestrator from cfg, pre-warms all Ollama layers, and returns
// it ready for use. Unavailable layers are marked down; they remain in the fallback chain.
func New(cfg *config.Config, logger *slog.Logger) (*Orchestrator, error) {
	// Build layer map from the config's named layer fields.
	layerMap := map[string]config.LayerConfig{
		"layer1": cfg.Layers.Layer1,
		"layer2": cfg.Layers.Layer2,
		"layer3": cfg.Layers.Layer3,
	}

	providerMap := make(map[string]sdk.Provider, 3)
	for name, lc := range layerMap {
		p, err := providers.New(lc)
		if err != nil {
			return nil, fmt.Errorf("orchestrator: build provider for %s: %w", name, err)
		}
		providerMap[name] = p
	}

	// Compile routing rules.
	rules := make([]compiledRule, 0, len(cfg.Orchestrator.Rules))
	for _, r := range cfg.Orchestrator.Rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("orchestrator: compile rule pattern %q: %w", r.Pattern, err)
		}
		rules = append(rules, compiledRule{pattern: re, layer: r.Layer})
	}

	o := &Orchestrator{
		rules:        rules,
		defaultLayer: cfg.Orchestrator.DefaultLayer,
		layers:       providerMap,
		layerCfgs:    layerMap,
		available:    make(map[string]bool, 3),
		logger:       logger,
	}

	// Pre-warm Ollama layers concurrently.
	o.warmup(layerMap)

	return o, nil
}

// warmup sends a minimal request to each Ollama layer to verify it is loaded.
func (o *Orchestrator) warmup(layerMap map[string]config.LayerConfig) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	for name, lc := range layerMap {
		if lc.Provider != "ollama" {
			mu.Lock()
			o.available[name] = true // cloud layers are assumed available at startup
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(layerName string, provider sdk.Provider) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err := provider.Ping(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				o.available[layerName] = false
				o.logger.Warn("layer unavailable during warmup",
					"layer", layerName,
					"reason", err.Error(),
				)
			} else {
				o.available[layerName] = true
			}
		}(name, o.layers[name])
	}
	wg.Wait()
}

// Classify returns the layer name for the given prompt by matching routing rules in order.
// If no rule matches, the default layer is returned.
func (o *Orchestrator) Classify(prompt string) string {
	for _, r := range o.rules {
		if r.pattern.MatchString(prompt) {
			return r.layer
		}
	}
	return o.defaultLayer
}

// Dispatch classifies the prompt, then sends req to the appropriate layer,
// following the fallback chain until a layer responds or all are exhausted.
// If ctx carries an episode ID (via episodic.WithEpisodeID) and an event store
// is attached, orchestrator_route, llm_call, and llm_response events are recorded.
func (o *Orchestrator) Dispatch(ctx context.Context, prompt string, req sdk.Request) (sdk.Response, error) {
	layer := o.Classify(prompt)
	episodeID, hasEpisode := episodic.EpisodeIDFromContext(ctx)

	for layer != "" {
		if !o.available[layer] {
			next := o.layerCfgs[layer].FallbackTo
			o.logger.Warn("skipping unavailable layer",
				"event", "fallback",
				"from", layer,
				"to", next,
				"reason", "layer marked unavailable",
			)
			layer = next
			continue
		}

		layerNum := layerNumber(layer)
		model := o.layerCfgs[layer].Model

		o.writeEvent(hasEpisode, episodeID, episodic.Event{
			Type:    episodic.TypeOrchestratorRoute,
			Layer:   layerNum,
			Model:   model,
			Payload: map[string]any{"layer": layer, "prompt_len": len(prompt)},
		})
		o.writeEvent(hasEpisode, episodeID, episodic.Event{
			Type:  episodic.TypeLLMCall,
			Layer: layerNum,
			Model: model,
		})

		start := time.Now()
		resp, err := o.layers[layer].Chat(ctx, req)
		latency := time.Since(start).Milliseconds()

		if err == nil {
			o.writeEvent(hasEpisode, episodeID, episodic.Event{
				Type:      episodic.TypeLLMResponse,
				Layer:     layerNum,
				Model:     resp.Model,
				LatencyMs: latency,
				TokensIn:  resp.TokensIn,
				TokensOut: resp.TokensOut,
				Payload:   map[string]any{"content": resp.Content},
			})
			return resp, nil
		}

		next := o.layerCfgs[layer].FallbackTo
		o.logger.Warn("layer error, falling back",
			"event", "fallback",
			"from", layer,
			"to", next,
			"reason", err.Error(),
		)
		layer = next
	}

	return sdk.Response{}, fmt.Errorf("orchestrator: all layers exhausted")
}

// writeEvent records an event to the episodic store when conditions are met.
func (o *Orchestrator) writeEvent(hasEpisode bool, episodeID string, ev episodic.Event) {
	if !hasEpisode || o.events == nil {
		return
	}
	if err := o.events.AppendEvent(episodeID, ev); err != nil {
		o.logger.Warn("orchestrator: write event failed", "type", ev.Type, "err", err)
	}
}

func layerNumber(name string) int {
	switch name {
	case "layer1":
		return 1
	case "layer2":
		return 2
	case "layer3":
		return 3
	default:
		return 0
	}
}

// DispatchStream classifies the prompt, then streams the response from the
// appropriate layer, following the fallback chain on error.
func (o *Orchestrator) DispatchStream(ctx context.Context, prompt string, req sdk.Request, out chan<- string) error {
	layer := o.Classify(prompt)

	for layer != "" {
		if !o.available[layer] {
			layer = o.layerCfgs[layer].FallbackTo
			continue
		}

		err := o.layers[layer].ChatStream(ctx, req, out)
		if err == nil {
			return nil
		}

		next := o.layerCfgs[layer].FallbackTo
		o.logger.Warn("layer stream error, falling back",
			"event", "fallback",
			"from", layer,
			"to", next,
			"reason", err.Error(),
		)
		layer = next
	}

	return fmt.Errorf("orchestrator: all layers exhausted")
}

// Status returns a snapshot of layer availability.
func (o *Orchestrator) Status() map[string]bool {
	out := make(map[string]bool, len(o.available))
	for k, v := range o.available {
		out[k] = v
	}
	return out
}
