// Package config loads and validates the Nexus YAML configuration file.
// Use Load to read a config from disk; the returned Config mirrors the full
// v1 schema and every required field is validated via struct tags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// Config is the top-level Nexus configuration struct.
type Config struct {
	Server      ServerConfig      `yaml:"server"      validate:"required"`
	Auth        AuthConfig        `yaml:"auth"`
	Layers      LayersConfig      `yaml:"layers"      validate:"required"`
	Orchestrator OrchestratorConfig `yaml:"orchestrator" validate:"required"`
	Governance  GovernanceConfig  `yaml:"governance"`
	Memory      MemoryConfig      `yaml:"memory"`
	Logging     LoggingConfig     `yaml:"logging"     validate:"required"`
	Metrics     MetricsConfig     `yaml:"metrics"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port     int  `yaml:"port"      validate:"required,min=1,max=65535"`
	LocalDev bool `yaml:"local_dev"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	APIKeys []APIKeyConfig `yaml:"api_keys"`
}

// APIKeyConfig represents a single API key entry.
type APIKeyConfig struct {
	Key          string `yaml:"key"            validate:"required"`
	Name         string `yaml:"name"           validate:"required"`
	RateLimitRPM int    `yaml:"rate_limit_rpm"`
}

// LayersConfig holds the ordered provider layers.
type LayersConfig struct {
	Layer1 LayerConfig `yaml:"layer1" validate:"required"`
	Layer2 LayerConfig `yaml:"layer2" validate:"required"`
	Layer3 LayerConfig `yaml:"layer3" validate:"required"`
}

// LayerConfig represents a single provider layer.
type LayerConfig struct {
	Provider   string        `yaml:"provider"    validate:"required,oneof=ollama anthropic google openai"`
	BaseURL    string        `yaml:"base_url"    validate:"required"`
	Model      string        `yaml:"model"       validate:"required"`
	Timeout    time.Duration `yaml:"timeout"     validate:"required"`
	APIKey     string        `yaml:"api_key"`
	FallbackTo string        `yaml:"fallback_to"`
}

// OrchestratorConfig holds request routing settings.
type OrchestratorConfig struct {
	Strategy     string        `yaml:"strategy"      validate:"required,oneof=keyword regex"`
	Rules        []RoutingRule `yaml:"rules"`
	DefaultLayer string        `yaml:"default_layer" validate:"required"`
}

// RoutingRule maps a regex pattern to a provider layer.
type RoutingRule struct {
	Pattern string `yaml:"pattern" validate:"required"`
	Layer   string `yaml:"layer"   validate:"required"`
}

// GovernanceConfig holds guardrail settings for inputs and outputs.
type GovernanceConfig struct {
	Input  InputGovernance  `yaml:"input"`
	Output OutputGovernance `yaml:"output"`
	Cost   CostGovernance   `yaml:"cost"`
}

// InputGovernance holds settings for input-side guardrails.
type InputGovernance struct {
	Enabled bool          `yaml:"enabled"`
	Rules   []GuardRule   `yaml:"rules"`
}

// GuardRule is a single regex-based guardrail rule.
type GuardRule struct {
	Name    string `yaml:"name"    validate:"required"`
	Pattern string `yaml:"pattern" validate:"required"`
	Action  string `yaml:"action"  validate:"required,oneof=block redact warn"`
}

// OutputGovernance holds settings for output-side guardrails.
type OutputGovernance struct {
	Enabled             bool     `yaml:"enabled"`
	MaxTokens           int      `yaml:"max_tokens"`
	DisallowedPatterns  []string `yaml:"disallowed_patterns"`
}

// CostGovernance holds token-budget enforcement settings.
type CostGovernance struct {
	MaxTokensPerRequest  int `yaml:"max_tokens_per_request"`
	WarnSessionTokens    int `yaml:"warn_session_tokens"`
	BlockSessionTokens   int `yaml:"block_session_tokens"`
}

// MemoryConfig holds settings for all memory tiers.
type MemoryConfig struct {
	Working  WorkingMemoryConfig  `yaml:"working"`
	Episodic EpisodicMemoryConfig `yaml:"episodic"`
	Semantic SemanticMemoryConfig `yaml:"semantic"`
	Archival ArchivalMemoryConfig `yaml:"archival"`
}

// WorkingMemoryConfig holds settings for the working-memory tier.
type WorkingMemoryConfig struct {
	TTL time.Duration `yaml:"ttl"`
}

// EpisodicMemoryConfig holds settings for the episodic-memory tier.
type EpisodicMemoryConfig struct {
	DB                  string        `yaml:"db"`
	OpenEpisodeTimeout  time.Duration `yaml:"open_episode_timeout"`
	ExtractionStrategy  string        `yaml:"extraction_strategy"`
	VectorIndex         string        `yaml:"vector_index"`
}

// SemanticMemoryConfig holds settings for the semantic-memory tier.
type SemanticMemoryConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ArchivalMemoryConfig holds settings for the archival-memory tier.
type ArchivalMemoryConfig struct {
	Enabled bool `yaml:"enabled"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"  validate:"required,oneof=debug info warn error"`
	Format string `yaml:"format" validate:"required,oneof=json text"`
	Output string `yaml:"output" validate:"required"`
}

// MetricsConfig holds Prometheus metrics settings.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

var validate = validator.New()

// Load reads a YAML config file from path, decodes it into a Config struct,
// and validates all required fields. It returns an error if the file cannot
// be read, the YAML is malformed, or validation fails.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}

	if err := validate.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("config: validation failed: %w", err)
	}

	return &cfg, nil
}

// DefaultConfigPath returns the default config file path (~/.nexus/config.yaml)
// with the home directory expanded.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.nexus/config.yaml"
	}
	return filepath.Join(home, ".nexus", "config.yaml")
}
