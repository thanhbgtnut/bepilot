package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIConfig configures the OpenAI-compatible Transcriber.
type OpenAIConfig struct {
	// BaseURL ends in the API version, e.g. http://localhost:1234/v1 (LM
	// Studio), https://openrouter.ai/api/v1, https://api.groq.com/openai/v1.
	BaseURL string
	APIKey  string
	Model   string
	// MaxTokens caps the answer (default 4096).
	MaxTokens   int
	Temperature float64
	// Timeout of one call (default 5 minutes).
	Timeout    time.Duration
	HTTPClient *http.Client
}

// OpenAI is a Transcriber that calls POST {BaseURL}/chat/completions with
// the prompt and the page image in one user message.
type OpenAI struct {
	cfg OpenAIConfig
	hc  *http.Client
}

// NewOpenAI returns the client.
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &OpenAI{cfg: cfg, hc: hc}
}

// Model implements Transcriber.
func (c *OpenAI) Model() string { return c.cfg.Model }

// Health reports a missing base URL or model; the model is exercised by the
// first page.
func (c *OpenAI) Health(context.Context) error {
	if c.cfg.BaseURL == "" || c.cfg.Model == "" {
		return errors.New("vlm: openai base_url and model are required")
	}
	return nil
}

// Transcribe implements Transcriber.
func (c *OpenAI) Transcribe(ctx context.Context, req Request) (*Transcription, error) {
	type part struct {
		Type     string            `json:"type"`
		Text     string            `json:"text,omitempty"`
		ImageURL map[string]string `json:"image_url,omitempty"`
	}
	type message struct {
		Role    string `json:"role"`
		Content []part `json:"content"`
	}
	body, err := json.Marshal(map[string]any{
		"model": c.cfg.Model, "max_tokens": c.cfg.MaxTokens, "temperature": c.cfg.Temperature,
		"messages": []message{{Role: "user", Content: []part{
			{Type: "text", Text: req.Prompt},
			{Type: "image_url", ImageURL: map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(req.JPEG)}},
		}}},
	})
	if err != nil {
		return nil, &PermanentError{Err: err}
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, &PermanentError{Err: err}
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("vlm: openai: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("vlm: openai: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("vlm: openai: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)])))
		// A 4xx other than 429 is the request's fault: a retry cannot fix it.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, &PermanentError{Err: err}
		}
		return nil, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("vlm: openai: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("vlm: openai: no choice in the answer")
	}
	meta, text := SplitFrontMatter(out.Choices[0].Message.Content)
	return &Transcription{Text: text, Meta: meta, PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens,
		Truncated: out.Choices[0].FinishReason == "length"}, nil
}
