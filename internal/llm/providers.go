package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	arkmodel "github.com/cloudwego/eino-ext/components/model/ark"
	claudemodel "github.com/cloudwego/eino-ext/components/model/claude"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/thanhenti/bepilot/internal/config"
)

// --- Claude / Anthropic -----------------------------------------------------

type claudeProvider struct {
	name    string
	cfg     config.ProviderCfg
	timeout time.Duration
}

func (p *claudeProvider) Name() string { return p.name }
func (p *claudeProvider) Kind() string { return "claude" }

func (p *claudeProvider) Model(ctx context.Context, modelID string, opt Options) (model.ToolCallingChatModel, error) {
	if opt.MaxTokens <= 0 {
		opt.MaxTokens = 4096
	}
	c := &claudemodel.Config{
		APIKey:         p.cfg.APIKey,
		Model:          modelID,
		MaxTokens:      opt.MaxTokens,
		Temperature:    opt.Temperature,
		RequestTimeout: p.timeout,
	}
	if p.cfg.BaseURL != "" {
		base := p.cfg.BaseURL
		c.BaseURL = &base
	}
	cm, err := claudemodel.NewChatModel(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("claude provider %q: %w", p.name, err)
	}
	return cm, nil
}

// --- OpenAI-compatible ----------------------------------------------------—-

type openaiProvider struct {
	name    string
	cfg     config.ProviderCfg
	timeout time.Duration
}

func (p *openaiProvider) Name() string { return p.name }
func (p *openaiProvider) Kind() string { return "openai" }

func (p *openaiProvider) Model(ctx context.Context, modelID string, opt Options) (model.ToolCallingChatModel, error) {
	apiKey := p.cfg.APIKey
	baseURL := normalizeOpenAIBaseURL(p.cfg.BaseURL)
	// Local OpenAI-compatible servers (LM Studio, llama.cpp, vLLM, Ollama) often
	// need no key; the go-openai client still sends an Authorization header, so
	// use a harmless placeholder when the operator left the key blank.
	if apiKey == "" {
		apiKey = "sk-no-key"
	}
	c := &openaimodel.ChatModelConfig{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       modelID,
		Temperature: opt.Temperature,
		Timeout:     p.timeout,
	}
	if opt.MaxTokens > 0 {
		mt := opt.MaxTokens
		c.MaxTokens = &mt
	}
	cm, err := openaimodel.NewChatModel(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("openai provider %q: %w", p.name, err)
	}
	return cm, nil
}

// normalizeOpenAIBaseURL accepts either the API base ("http://host:port/v1") or
// a full endpoint URL ("http://host:port/v1/chat/completions") and returns the
// base, since the client appends the endpoint path itself.
func normalizeOpenAIBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/chat/completions", "/completions", "/embeddings", "/responses"} {
		u = strings.TrimSuffix(u, suffix)
	}
	return strings.TrimRight(u, "/")
}

// --- Volcengine Ark / Doubao ---------------------------------------------—-—

type arkProvider struct {
	name string
	cfg  config.ProviderCfg
}

func (p *arkProvider) Name() string { return p.name }
func (p *arkProvider) Kind() string { return "ark" }

func (p *arkProvider) Model(ctx context.Context, modelID string, opt Options) (model.ToolCallingChatModel, error) {
	c := &arkmodel.ChatModelConfig{
		APIKey:      p.cfg.APIKey,
		BaseURL:     p.cfg.BaseURL,
		Region:      p.cfg.Region,
		Model:       modelID,
		Temperature: opt.Temperature,
	}
	if opt.MaxTokens > 0 {
		mt := opt.MaxTokens
		c.MaxTokens = &mt
	}
	cm, err := arkmodel.NewChatModel(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("ark provider %q: %w", p.name, err)
	}
	return cm, nil
}
