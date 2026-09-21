// Package config loads runtime configuration from a YAML file with ${ENV}
// expansion, then applies a small set of well-known environment overrides.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
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
	// File is the path to the server-list YAML (see MCPServersFile). Empty
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

// MCPServersFile is the schema of the file referenced by MCPCfg.File.
type MCPServersFile struct {
	Servers []MCPServerSpec `yaml:"servers"`
}

// MCPServerSpec describes one MCP server connection. It is used both for the
// file source and as the persisted/API shape.
type MCPServerSpec struct {
	Name      string `yaml:"name" json:"name"`
	Transport string `yaml:"transport" json:"transport"` // stdio | sse | streamable_http
	Disabled  bool   `yaml:"disabled" json:"disabled,omitempty"`

	// stdio transport
	Command string            `yaml:"command" json:"command,omitempty"`
	Args    []string          `yaml:"args" json:"args,omitempty"`
	Env     map[string]string `yaml:"env" json:"env,omitempty"`

	// sse / streamable_http transport
	URL     string            `yaml:"url" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`

	// ToolAllowlist, when non-empty, imports only these tool names from the
	// server (matched against the server-side name, before prefixing).
	ToolAllowlist []string `yaml:"tool_allowlist" json:"tool_allowlist,omitempty"`
}

// Validate checks a single server spec.
func (s MCPServerSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("mcp server: name is required")
	}
	for _, r := range s.Name {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return fmt.Errorf("mcp server %q: name may only contain letters, digits, '-' and '_'", s.Name)
		}
	}
	switch s.Transport {
	case "stdio":
		if s.Command == "" {
			return fmt.Errorf("mcp server %q: command is required for the stdio transport", s.Name)
		}
	case "sse", "streamable_http":
		if s.URL == "" {
			return fmt.Errorf("mcp server %q: url is required for the %s transport", s.Name, s.Transport)
		}
	default:
		return fmt.Errorf("mcp server %q: transport %q is not supported (want stdio | sse | streamable_http)", s.Name, s.Transport)
	}
	return nil
}

// HTTP holds the Hertz server settings.
type HTTP struct {
	Addr            string        `yaml:"addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	CORSOrigins     []string      `yaml:"cors_origins"`
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

// ProviderCfg configures one LLM provider implementation.
type ProviderCfg struct {
	Kind    string `yaml:"kind"` // claude | openai | ark
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Region  string `yaml:"region"` // ark / bedrock
}

// LLM configures the provider registry and default model selection.
type LLM struct {
	DefaultProvider string `yaml:"default_provider"`
	DefaultModel    string `yaml:"default_model"`
	SummaryModel    string `yaml:"summary_model"`
	MaxTokens       int    `yaml:"max_tokens"`
	// Temperature is the default sampling temperature when a request does not
	// set one. Unset = the provider's own default. Lower it (0–0.3) where the
	// same request should give the same answer every time.
	Temperature    *float32               `yaml:"temperature"`
	RequestTimeout time.Duration          `yaml:"request_timeout"`
	Providers      map[string]ProviderCfg `yaml:"providers"`
}

// Embedding configures the embedder used for skill retrieval.
type Embedding struct {
	Kind    string `yaml:"kind"` // hash | openai
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Dim     int    `yaml:"dim"`
}

// AgentCfg holds knobs that shape agent behaviour.
type AgentCfg struct {
	Identity           string        `yaml:"identity"`
	ResponseStyle      string        `yaml:"response_style"`
	MaxSteps           int           `yaml:"max_steps"`
	HistoryTokenBudget int           `yaml:"history_token_budget"`
	SummarizeEveryN    int           `yaml:"summarize_every_n"`
	SkillTopK          int           `yaml:"skill_top_k"`
	PingInterval       time.Duration `yaml:"ping_interval"`
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

	setString(&c.Agent.Identity, defaultIdentity)
	setString(&c.Agent.ResponseStyle, defaultResponseStyle)
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

const defaultIdentity = `You are bepilot, a helpful, precise AI assistant. You give direct, well-structured answers, use tools when they materially improve the result, and you never fabricate facts or tool output.`

const defaultResponseStyle = `Prefer concise, skimmable answers. Use Markdown. Lead with the answer, then supporting detail. Match the user's language.`
