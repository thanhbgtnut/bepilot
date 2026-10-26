// Package config loads runtime configuration from a YAML file with ${ENV}
// expansion, then applies a small set of well-known environment overrides.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thanhenti/bepilot/agentcore/agent"
	"github.com/thanhenti/bepilot/agentcore/llm"
	"github.com/thanhenti/bepilot/agentcore/retrieval"
)

// Config is the root configuration object for the server.
type Config struct {
	Env    string    `yaml:"env"`
	HTTP   HTTP      `yaml:"http"`
	DB     DB        `yaml:"db"`
	LLM    LLM       `yaml:"llm"`
	Embd   Embedding `yaml:"embedding"`
	Agent  AgentCfg  `yaml:"agent"`
	Skills SkillsCfg `yaml:"skills"`
	Tools  ToolsCfg  `yaml:"tools"`
	MCP    MCPCfg    `yaml:"mcp"`
	Docs   Documents `yaml:"documents"`
	Log    Log       `yaml:"log"`
}

// ToolsCfg configures the built-in tool set.
type ToolsCfg struct {
	// HTTPAllowlist restricts http_fetch to these hosts. Empty allows any
	// public host (loopback/private ranges are always refused).
	HTTPAllowlist []string `yaml:"http_allowlist"`
}

// MCPCfg configures how Model Context Protocol servers are attached. The
// servers themselves are declared two ways, both hot-reloadable without a
// restart: statically in a YAML file (File, re-read every ReloadInterval) and
// dynamically via the /v1/mcp API (persisted in the database).
type MCPCfg struct {
	// Enabled turns the whole MCP subsystem on. When false, File and the API
	// are ignored and no MCP clients are started.
	Enabled bool `yaml:"enabled"`
	// File is the path to the server-list YAML (see mcp.ServersFile). Empty
	// disables the file source; the API still works.
	File string `yaml:"file"`
	// ReloadInterval is how often File's mtime is polled for changes. Each
	// change is reconciled against the running clients (add / remove / redial).
	// Zero means load once at startup and never re-read.
	ReloadInterval time.Duration `yaml:"reload_interval"`
	// InitTimeout bounds the connect + Initialize handshake for one server.
	InitTimeout time.Duration `yaml:"init_timeout"`
	// ToolPrefix is prepended to every imported tool name, with the server
	// name, as "<prefix><server>__<tool>". Defaults to "mcp__".
	ToolPrefix string `yaml:"tool_prefix"`
}

// Documents configures the document endpoints (/v1/documents/*), which read
// PDFs and images with docpipe: pages are rendered, read by TurboOCR and,
// when VLM.BaseURL is set, re-read by a vision model into markdown.
type Documents struct {
	// Enabled turns the endpoints on; when false they answer 503.
	Enabled bool `yaml:"enabled"`
	// RenderMode is the PDF renderer: webassembly (default, no cgo) or
	// multi_threaded (needs libpdfium and the pdfium-worker binary).
	RenderMode    string `yaml:"render_mode"`
	RenderWorkers int    `yaml:"render_workers"`
	// DPI renders PDF pages at this resolution (default 300).
	DPI int `yaml:"dpi"`
	// Concurrency is how many pages are read at once (default 4).
	Concurrency int `yaml:"concurrency"`
	// MaxFileMB caps one uploaded file (default 32).
	MaxFileMB int `yaml:"max_file_mb"`
	// MaxFiles caps the documents in one /v1/documents/messages request
	// (default 5).
	MaxFiles int         `yaml:"max_files"`
	OCR      DocumentOCR `yaml:"ocr"`
	VLM      DocumentVLM `yaml:"vlm"`

	// Workers is how many uploaded documents (/v1/documents) are read in the
	// background at once (default 2). A worker holds a document for Lease
	// (default 2m), renewed while it works; a document whose lease ran out is
	// taken over by another worker, which reads only the pages still missing.
	// A document that fails is retried MaxAttempts times (default 3).
	Workers      int           `yaml:"workers"`
	Lease        time.Duration `yaml:"lease"`
	MaxAttempts  int           `yaml:"max_attempts"`
	PollInterval time.Duration `yaml:"poll_interval"` // default 2s
	// WaitTimeout is how long a streamed question waits for the documents of
	// its session to be read (default 15m).
	WaitTimeout time.Duration `yaml:"wait_timeout"`
	// ContextWindow is the model context, in tokens, a question with
	// documents must fit in; ContextWindows overrides it per model. 0 skips
	// the check.
	ContextWindow  int            `yaml:"context_window"`
	ContextWindows map[string]int `yaml:"context_windows"`
}

// ContextWindowFor returns the context window configured for model, or 0.
func (d Documents) ContextWindowFor(model string) int {
	if n, ok := d.ContextWindows[model]; ok {
		return n
	}
	return d.ContextWindow
}

// DocumentOCR is the TurboOCR server (POST /ocr/raw).
type DocumentOCR struct {
	BaseURL string        `yaml:"base_url"`
	Timeout time.Duration `yaml:"timeout"`
}

// DocumentVLM is an OpenAI-compatible vision model that re-reads each page
// into markdown. Empty BaseURL = OCR only.
type DocumentVLM struct {
	BaseURL string        `yaml:"base_url"`
	APIKey  string        `yaml:"api_key"`
	Model   string        `yaml:"model"`
	Timeout time.Duration `yaml:"timeout"`
	// NumberCheck compares the numbers of the VLM markdown with the OCR
	// text: off (default), detect (mark conflicts unverified) or confirm
	// (ask the VLM about them in the same conversation; replace only when it
	// is sure). ConfirmPrompt overrides the follow-up question (%s is the
	// list of conflicts).
	NumberCheck   string `yaml:"number_check"`
	ConfirmPrompt string `yaml:"confirm_prompt"`
}

// HTTP holds the Hertz server settings.
type HTTP struct {
	Addr            string        `yaml:"addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	CORSOrigins     []string      `yaml:"cors_origins"`
	// MaxRequestBodyMB caps a request body (default 64). Document uploads
	// need it above Hertz's 4 MB default; base64 adds a third.
	MaxRequestBodyMB int `yaml:"max_request_body_mb"`
	// AuthBypass skips x-api-key verification entirely and treats every
	// request as a fixed local dev user. Dev-only — never set this in a
	// deployed environment.
	AuthBypass bool `yaml:"auth_bypass"`
}

// DB holds Postgres connection settings.
type DB struct {
	DSN             string        `yaml:"dsn"`
	MaxConns        int32         `yaml:"max_conns"`
	MinConns        int32         `yaml:"min_conns"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime"`
	AutoMigrate     bool          `yaml:"auto_migrate"`
}

// LLM configures the provider registry and default model selection.
type LLM = llm.Config

// ProviderCfg configures one LLM provider implementation.
type ProviderCfg = llm.ProviderConfig

// Embedding configures the embedder used for skill retrieval.
type Embedding = retrieval.Config

// AgentCfg holds knobs that shape agent behaviour, plus how often the HTTP
// layer pings an open stream.
type AgentCfg struct {
	agent.Config `yaml:",inline"`
	PingInterval time.Duration `yaml:"ping_interval"`
}

// SkillsCfg configures the file-based skill source.
type SkillsCfg struct {
	Dir           string `yaml:"dir"`
	SyncOnStartup bool   `yaml:"sync_on_startup"`
}

// Log configures structured logging.
type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // json | text
}

// Load reads the YAML file at path, expands ${ENV} references against the
// process environment, unmarshals it, applies overrides and defaults, and
// validates the result.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	expanded := os.Expand(string(raw), func(key string) string {
		// Support ${VAR:-default} style fallbacks.
		return os.Getenv(key)
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyEnvOverrides()
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("BEPILOT_HTTP_ADDR"); v != "" {
		c.HTTP.Addr = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		c.DB.DSN = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		p := c.LLM.Providers["claude"]
		if p.Kind == "" {
			p.Kind = "claude"
		}
		p.APIKey = v
		if c.LLM.Providers == nil {
			c.LLM.Providers = map[string]ProviderCfg{}
		}
		c.LLM.Providers["claude"] = p
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		p := c.LLM.Providers["openai"]
		if p.Kind == "" {
			p.Kind = "openai"
		}
		p.APIKey = v
		if c.LLM.Providers == nil {
			c.LLM.Providers = map[string]ProviderCfg{}
		}
		c.LLM.Providers["openai"] = p
	}
	if v := os.Getenv("BEPILOT_DEFAULT_PROVIDER"); v != "" {
		c.LLM.DefaultProvider = v
	}
}

func (c *Config) applyDefaults() {
	setString(&c.Env, "development")
	setString(&c.HTTP.Addr, ":8080")
	setDuration(&c.HTTP.ReadTimeout, 30*time.Second)
	setDuration(&c.HTTP.WriteTimeout, 0) // 0 = no write timeout, required for SSE
	setDuration(&c.HTTP.ShutdownTimeout, 20*time.Second)
	setInt(&c.HTTP.MaxRequestBodyMB, 64)

	setInt32(&c.DB.MaxConns, 10)
	setInt32(&c.DB.MinConns, 1)
	setDuration(&c.DB.MaxConnLifetime, time.Hour)

	setString(&c.LLM.DefaultProvider, "claude")
	setString(&c.LLM.DefaultModel, "claude-sonnet-5")
	setString(&c.LLM.SummaryModel, c.LLM.DefaultModel)
	setInt(&c.LLM.MaxTokens, 4096)
	setDuration(&c.LLM.RequestTimeout, 5*time.Minute)
	if c.LLM.Providers == nil {
		c.LLM.Providers = map[string]ProviderCfg{}
	}

	setString(&c.Embd.Kind, "hash")
	setInt(&c.Embd.Dim, 1536)

	setString(&c.Agent.Identity, agent.DefaultIdentity)
	setString(&c.Agent.ResponseStyle, agent.DefaultResponseStyle)
	setInt(&c.Agent.MaxSteps, 16)
	setInt(&c.Agent.HistoryTokenBudget, 24000)
	setInt(&c.Agent.SummarizeEveryN, 8)
	setInt(&c.Agent.SkillTopK, 4)
	setDuration(&c.Agent.PingInterval, 15*time.Second)

	setString(&c.Skills.Dir, "skills")

	if c.MCP.Enabled {
		setString(&c.MCP.ToolPrefix, "mcp__")
		setDuration(&c.MCP.InitTimeout, 20*time.Second)
		if c.MCP.File != "" {
			setDuration(&c.MCP.ReloadInterval, 10*time.Second)
		}
	}

	if c.Docs.Enabled {
		setString(&c.Docs.RenderMode, "webassembly")
		setInt(&c.Docs.RenderWorkers, 2)
		setInt(&c.Docs.DPI, 300)
		setInt(&c.Docs.Concurrency, 4)
		setInt(&c.Docs.MaxFileMB, 32)
		setInt(&c.Docs.MaxFiles, 5)
		setDuration(&c.Docs.OCR.Timeout, 2*time.Minute)
		setDuration(&c.Docs.VLM.Timeout, 5*time.Minute)
		setInt(&c.Docs.Workers, 2)
		setDuration(&c.Docs.Lease, 2*time.Minute)
		setInt(&c.Docs.MaxAttempts, 3)
		setDuration(&c.Docs.PollInterval, 2*time.Second)
		setDuration(&c.Docs.WaitTimeout, 15*time.Minute)
	}

	setString(&c.Log.Level, "info")
	setString(&c.Log.Format, "json")
}

func (c *Config) validate() error {
	if c.DB.DSN == "" {
		return fmt.Errorf("db.dsn (or DATABASE_URL) is required")
	}
	if len(c.LLM.Providers) == 0 {
		return fmt.Errorf("llm.providers must contain at least one provider")
	}
	if _, ok := c.LLM.Providers[c.LLM.DefaultProvider]; !ok {
		return fmt.Errorf("llm.default_provider %q has no matching entry in llm.providers", c.LLM.DefaultProvider)
	}
	for name, p := range c.LLM.Providers {
		switch p.Kind {
		case "claude", "openai", "ark", "fake":
		default:
			return fmt.Errorf("llm.providers.%s.kind %q is not supported", name, p.Kind)
		}
	}
	if c.Docs.Enabled && c.Docs.OCR.BaseURL == "" {
		return fmt.Errorf("documents.ocr.base_url is required when documents.enabled is true")
	}
	if c.Docs.VLM.BaseURL != "" && c.Docs.VLM.Model == "" {
		return fmt.Errorf("documents.vlm.model is required when documents.vlm.base_url is set")
	}
	if c.Embd.Kind != "hash" && c.Embd.Kind != "openai" {
		return fmt.Errorf("embedding.kind %q is not supported", c.Embd.Kind)
	}
	return nil
}

func setString(p *string, def string) {
	if *p == "" {
		*p = def
	}
}
func setInt(p *int, def int) {
	if *p == 0 {
		*p = def
	}
}
func setInt32(p *int32, def int32) {
	if *p == 0 {
		*p = def
	}
}
func setDuration(p *time.Duration, def time.Duration) {
	if *p == 0 {
		*p = def
	}
}
