// Package domain holds the conversation entities the agent reads and writes:
// users, sessions, messages and their content blocks, skills and per-turn run
// records. They carry no storage concerns; a Store implementation decides how
// they are kept.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role values used on messages, matching the Anthropic message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Content block types, matching the Anthropic content block types.
const (
	BlockText       = "text"
	BlockThinking   = "thinking"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
)

// User is the principal a turn runs for.
type User struct {
	ID        uuid.UUID
	Email     string
	Name      string
	CreatedAt time.Time
}

// Session is a conversation thread owned by a User.
type Session struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Title          string
	Provider       string
	Model          string
	SystemOverride string
	Summary        string
	Metadata       map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

// Message is a single turn in a Session. Its content is an ordered list of
// ContentBlocks.
type Message struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	Seq        int
	Role       string
	StopReason string
	Usage      Usage
	CreatedAt  time.Time
	Blocks     []ContentBlock
}

// Usage records token accounting for an assistant message.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ContentBlock is one element of a Message's content array.
type ContentBlock struct {
	ID         uuid.UUID
	MessageID  uuid.UUID
	Idx        int
	Type       string
	Text       string
	Thinking   string
	ToolName   string
	ToolUseID  string
	ToolInput  map[string]any
	ToolResult any
	IsError    bool
}

// Skill is a file-based capability document, indexed for retrieval.
type Skill struct {
	ID           uuid.UUID
	Slug         string
	Name         string
	Description  string
	Path         string
	Body         string
	Checksum     string
	AllowedTools []string
	Enabled      bool
	HasEmbedding bool
	UpdatedAt    time.Time
}

// SkillMatch is a retrieval hit with its cosine similarity in [0,1].
type SkillMatch struct {
	Skill
	Score float64
}

// AgentRun is an observability record for one assistant turn.
type AgentRun struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	MessageID *uuid.UUID
	Provider  string
	Model     string
	Steps     int
	TokensIn  int
	TokensOut int
	LatencyMS int
	Error     string
	// Detail is how the turn ran: stop reason, sampling settings, tool calls,
	// steering. Free-form so new observations need no migration.
	Detail    map[string]any
	CreatedAt time.Time
}
