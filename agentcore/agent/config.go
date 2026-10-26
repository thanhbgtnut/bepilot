package agent

// Config holds the knobs that shape agent behaviour.
type Config struct {
	// Identity opens the system prompt. Defaults to DefaultIdentity.
	Identity string `yaml:"identity"`
	// ResponseStyle is the prompt's style section. Defaults to
	// DefaultResponseStyle.
	ResponseStyle string `yaml:"response_style"`
	// MaxSteps is the most model calls one turn may make. Defaults to 16.
	MaxSteps int `yaml:"max_steps"`
	// HistoryTokenBudget caps the (approximate) tokens of history sent to the
	// model. Defaults to 24000.
	HistoryTokenBudget int `yaml:"history_token_budget"`
	// SummarizeEveryN refreshes the session's rolling summary every N
	// messages. Zero turns summaries off.
	SummarizeEveryN int `yaml:"summarize_every_n"`
	// SkillTopK is how many retrieved skills are named in the prompt. Defaults
	// to 4.
	SkillTopK int `yaml:"skill_top_k"`
}

// DefaultIdentity is the identity used when Config.Identity is empty.
const DefaultIdentity = `You are bepilot, a helpful, precise AI assistant. You give direct, well-structured answers, use tools when they materially improve the result, and you never fabricate facts or tool output.`

// DefaultResponseStyle is the style used when Config.ResponseStyle is empty.
const DefaultResponseStyle = `Prefer concise, skimmable answers. Use Markdown. Lead with the answer, then supporting detail. Match the user's language.`

// WithDefaults returns c with every unset field (except SummarizeEveryN) given
// its default.
func (c Config) WithDefaults() Config {
	if c.Identity == "" {
		c.Identity = DefaultIdentity
	}
	if c.ResponseStyle == "" {
		c.ResponseStyle = DefaultResponseStyle
	}
	if c.MaxSteps <= 0 {
		c.MaxSteps = 16
	}
	if c.HistoryTokenBudget <= 0 {
		c.HistoryTokenBudget = 24000
	}
	if c.SkillTopK <= 0 {
		c.SkillTopK = 4
	}
	return c
}
