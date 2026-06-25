package orchestrator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"log/slog"
	"os"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
	"github.com/vishnuvardhansankoti/ai-nexus/orchestrator"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

// mockProvider is an inline sdk.Provider for testing.
type mockProvider struct {
	pingErr  error
	chatResp sdk.Response
	chatErr  error
	calls    int
}

func (m *mockProvider) Chat(_ context.Context, _ sdk.Request) (sdk.Response, error) {
	m.calls++
	return m.chatResp, m.chatErr
}

func (m *mockProvider) ChatStream(_ context.Context, _ sdk.Request, out chan<- string) error {
	defer close(out)
	if m.chatErr != nil {
		return m.chatErr
	}
	out <- m.chatResp.Content
	return nil
}

func (m *mockProvider) Ping(_ context.Context) error { return m.pingErr }

// buildTestConfig returns a minimal config with keyword rules.
func buildTestConfig() *config.Config {
	return &config.Config{
		Layers: config.LayersConfig{
			Layer1: config.LayerConfig{Provider: "ollama", BaseURL: "http://mock1", Model: "model1", Timeout: time.Second},
			Layer2: config.LayerConfig{Provider: "ollama", BaseURL: "http://mock2", Model: "model2", Timeout: time.Second, FallbackTo: ""},
			Layer3: config.LayerConfig{Provider: "ollama", BaseURL: "http://mock3", Model: "model3", Timeout: time.Second},
		},
		Orchestrator: config.OrchestratorConfig{
			Strategy:     "keyword",
			DefaultLayer: "layer2",
			Rules: []config.RoutingRule{
				{Pattern: `(what is|define)`, Layer: "layer1"},
				{Pattern: `(architect|design)`, Layer: "layer3"},
			},
		},
		Server:  config.ServerConfig{Port: 11434},
		Logging: config.LoggingConfig{Level: "info", Format: "json", Output: "stdout"},
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// newWithMocks builds an orchestrator but bypasses provider construction
// by providing mock providers through a test-friendly constructor.
func newWithMocks(cfg *config.Config, mockLayers map[string]sdk.Provider) *orchestrator.TestOrchestrator {
	return orchestrator.NewWithMocks(cfg, mockLayers, silentLogger())
}

func TestClassify_matchesFirstRule(t *testing.T) {
	cfg := buildTestConfig()
	o := orchestrator.NewWithMocks(cfg, nil, silentLogger())

	layer := o.Classify("what is Go")
	if layer != "layer1" {
		t.Errorf("expected layer1, got %q", layer)
	}
}

func TestClassify_defaultLayer(t *testing.T) {
	cfg := buildTestConfig()
	o := orchestrator.NewWithMocks(cfg, nil, silentLogger())

	layer := o.Classify("unmatched prompt about nothing")
	if layer != "layer2" {
		t.Errorf("expected layer2 (default), got %q", layer)
	}
}

func TestDispatch_routesToCorrectLayer(t *testing.T) {
	cfg := buildTestConfig()
	mock1 := &mockProvider{chatResp: sdk.Response{Content: "from layer1"}, pingErr: nil}
	mock2 := &mockProvider{chatResp: sdk.Response{Content: "from layer2"}, pingErr: nil}
	mock3 := &mockProvider{chatResp: sdk.Response{Content: "from layer3"}, pingErr: nil}

	mocks := map[string]sdk.Provider{"layer1": mock1, "layer2": mock2, "layer3": mock3}
	o := orchestrator.NewWithMocks(cfg, mocks, silentLogger())

	resp, err := o.Dispatch(context.Background(), "what is Go", sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "what is Go"}},
	})
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if resp.Content != "from layer1" {
		t.Errorf("expected layer1 response, got %q", resp.Content)
	}
	if mock1.calls != 1 {
		t.Errorf("expected 1 call to layer1, got %d", mock1.calls)
	}
}

func TestDispatch_fallback(t *testing.T) {
	cfg := buildTestConfig()
	// layer1 is configured with fallback_to=layer2 in the test config; set it up.
	cfg.Layers.Layer1 = config.LayerConfig{
		Provider: "ollama", BaseURL: "http://mock1", Model: "m1",
		Timeout: time.Second, FallbackTo: "layer2",
	}

	mock1 := &mockProvider{chatErr: errors.New("layer1 down"), pingErr: nil}
	mock2 := &mockProvider{chatResp: sdk.Response{Content: "fallback response"}, pingErr: nil}
	mock3 := &mockProvider{chatResp: sdk.Response{Content: "from layer3"}, pingErr: nil}

	mocks := map[string]sdk.Provider{"layer1": mock1, "layer2": mock2, "layer3": mock3}
	o := orchestrator.NewWithMocks(cfg, mocks, silentLogger())

	resp, err := o.Dispatch(context.Background(), "what is Go", sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "what is Go"}},
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if resp.Content != "fallback response" {
		t.Errorf("expected fallback response, got %q", resp.Content)
	}
	if mock1.calls != 1 {
		t.Errorf("expected 1 call to layer1, got %d", mock1.calls)
	}
	if mock2.calls != 1 {
		t.Errorf("expected 1 call to layer2, got %d", mock2.calls)
	}
}

func TestDispatch_allLayersExhausted(t *testing.T) {
	cfg := buildTestConfig()
	cfg.Layers.Layer1 = config.LayerConfig{
		Provider: "ollama", BaseURL: "http://mock1", Model: "m1",
		Timeout: time.Second, FallbackTo: "layer2",
	}
	cfg.Layers.Layer2 = config.LayerConfig{
		Provider: "ollama", BaseURL: "http://mock2", Model: "m2",
		Timeout: time.Second, FallbackTo: "",
	}

	errDown := errors.New("down")
	mocks := map[string]sdk.Provider{
		"layer1": &mockProvider{chatErr: errDown},
		"layer2": &mockProvider{chatErr: errDown},
		"layer3": &mockProvider{chatErr: errDown},
	}
	o := orchestrator.NewWithMocks(cfg, mocks, silentLogger())

	_, err := o.Dispatch(context.Background(), "what is Go", sdk.Request{
		Messages: []sdk.Message{{Role: "user", Content: "what is Go"}},
	})
	if err == nil {
		t.Fatal("expected error when all layers exhausted, got nil")
	}
}
