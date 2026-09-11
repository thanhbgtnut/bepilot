// Package domain holds the persistence-agnostic entities shared across the
// application. These structs mirror the database schema but carry no storage
// concerns.
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

// User is an authenticated principal that owns sessions.
type User struct {
	ID        uuid.UUID
	Email     string
	Name      string
	CreatedAt time.Time
}

// APIKey is a hashed credential bound to a User. The plaintext key is only ever
// seen at creation time.
type APIKey struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Prefix     string // first 12 chars, stored for lookup
	Hash       []byte // sha256(plaintext)
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
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

// Message is a single turn in a Session. Its content is stored as an ordered
// list of ContentBlock rows.
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

// Skill is a file-based capability document synced into the database.
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

// MCPServer is a Model Context Protocol server registered at runtime via the
// API. File-declared servers are not persisted here. The connection spec is
// stored verbatim as JSON in Spec; the top-level columns are for listing and
// lookup only.
type MCPServer struct {
	ID        uuid.UUID
	Name      string
	Transport string
	Enabled   bool
	Spec      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
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
	CreatedAt time.Time
}
