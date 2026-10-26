// Package retrieval provides text embedding for skill discovery. It ships a
// deterministic offline embedder (hash) and an OpenAI-compatible HTTP embedder.
package retrieval

import (
	"context"
	"fmt"
)

// Embedder turns text into dense vectors.
type Embedder interface {
	// Embed returns one vector per input string. All vectors have length Dim().
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim is the fixed output dimension.
	Dim() int
	// Name identifies the implementation for logging.
	Name() string
}

// Config selects and configures an Embedder.
type Config struct {
	Kind    string `yaml:"kind"` // hash | openai
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Dim     int    `yaml:"dim"`
}

// New builds an Embedder from cfg.
func New(cfg Config) (Embedder, error) {
	switch cfg.Kind {
	case "hash", "":
		return NewHashEmbedder(cfg.Dim), nil
	case "openai":
		// No api_key is required: local OpenAI-compatible servers accept none.
		return NewOpenAIEmbedder(cfg), nil
	default:
		return nil, fmt.Errorf("unknown embedding kind %q", cfg.Kind)
	}
}
