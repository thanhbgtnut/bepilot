// Package llm is a thin provider abstraction over eino-ext chat models. A
// Registry holds one Provider per configured entry; the agent asks the registry
// for a ToolCallingChatModel by (provider, model) at request time.
package llm

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
)

// ProviderConfig configures one LLM provider implementation.
type ProviderConfig struct {
	Kind    string `yaml:"kind"` // claude | openai | ark | fake
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Region  string `yaml:"region"` // ark / bedrock
}

// Config configures the provider registry and default model selection.
type Config struct {
	DefaultProvider string `yaml:"default_provider"`
	DefaultModel    string `yaml:"default_model"`
	SummaryModel    string `yaml:"summary_model"`
	MaxTokens       int    `yaml:"max_tokens"`
	// Temperature is the default sampling temperature when a request does not
	// set one. Unset = the provider's own default. Lower it (0–0.3) where the
	// same request should give the same answer every time.
	Temperature    *float32                  `yaml:"temperature"`
	RequestTimeout time.Duration             `yaml:"request_timeout"`
	Providers      map[string]ProviderConfig `yaml:"providers"`
}

// Options are per-request generation parameters.
type Options struct {
	MaxTokens   int
	Temperature *float32
}

// Provider builds chat models for one upstream (Anthropic, OpenAI, Ark, ...).
type Provider interface {
	Name() string
	Kind() string
	Model(ctx context.Context, modelID string, opt Options) (model.ToolCallingChatModel, error)
}

// Registry resolves providers by name.
type Registry struct {
	providers map[string]Provider
	def       string
}

// NewRegistry constructs providers from cfg.
func NewRegistry(cfg Config) (*Registry, error) {
	reg := &Registry{providers: map[string]Provider{}, def: cfg.DefaultProvider}
	for name, pc := range cfg.Providers {
		switch pc.Kind {
		case "claude":
			reg.providers[name] = &claudeProvider{name: name, cfg: pc, timeout: cfg.RequestTimeout}
		case "openai":
			reg.providers[name] = &openaiProvider{name: name, cfg: pc, timeout: cfg.RequestTimeout}
		case "ark":
			reg.providers[name] = &arkProvider{name: name, cfg: pc}
		case "fake":
			// Wired by the caller via Register to avoid an import cycle.
		default:
			return nil, fmt.Errorf("llm: unknown provider kind %q for %q", pc.Kind, name)
		}
	}
	return reg, nil
}

// Register adds or replaces a provider. Used to inject the fake provider.
func (r *Registry) Register(name string, p Provider) { r.providers[name] = p }

// Validate ensures the default provider is registered. Call after Register.
func (r *Registry) Validate() error {
	if _, ok := r.providers[r.def]; !ok {
		return fmt.Errorf("llm: default provider %q is not registered", r.def)
	}
	return nil
}

// Get returns the provider by name, or the default when name is empty.
func (r *Registry) Get(name string) (Provider, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = r.def
	}
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("llm: provider %q is not configured", name)
	}
	return p, nil
}

// DefaultName returns the configured default provider name.
func (r *Registry) DefaultName() string { return r.def }

// Names lists configured provider names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
