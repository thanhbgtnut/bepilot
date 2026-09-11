// Package llm is a thin provider abstraction over eino-ext chat models. A
// Registry holds one Provider per configured entry; the agent asks the registry
// for a ToolCallingChatModel by (provider, model) at request time.
package llm

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/model"

	"github.com/thanhenti/bepilot/internal/config"
)

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

// NewRegistry constructs providers from config.
func NewRegistry(cfg config.LLM) (*Registry, error) {
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
