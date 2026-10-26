// Package domain holds the persistence-agnostic entities shared across the
// application. The conversation entities come from the agentcore module and
// are re-exported here; the rest (API keys, MCP servers, task results) belong
// to the bepilot service only.
package domain

import (
	"time"

	"github.com/google/uuid"

	core "github.com/thanhenti/bepilot/agentcore/domain"
)

// Role values used on messages, matching the Anthropic message roles.
const (
	RoleUser      = core.RoleUser
	RoleAssistant = core.RoleAssistant
)

// Content block types, matching the Anthropic content block types.
const (
	BlockText       = core.BlockText
	BlockThinking   = core.BlockThinking
	BlockToolUse    = core.BlockToolUse
	BlockToolResult = core.BlockToolResult
)

// Conversation entities, defined by agentcore.
type (
	User         = core.User
	Session      = core.Session
	Message      = core.Message
	Usage        = core.Usage
	ContentBlock = core.ContentBlock
	Skill        = core.Skill
	SkillMatch   = core.SkillMatch
	AgentRun     = core.AgentRun
)

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

// TaskResult is one skill task's structured output for a session (e.g. a
// checklist review or an information-extraction pass). It is kept separate
// from the message transcript specifically so a client can read it — for a
// "full report" view — without going through the agent run that produced it:
// a plain row read, unaffected by the agent/LLM pipeline being broken or slow.
// A second save with the same (SessionID, TaskKey) replaces the previous one.
type TaskResult struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	TaskKey   string
	Title     string
	Result    map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Document request kinds and statuses.
const (
	DocumentKindParse   = "parse"
	DocumentKindMessage = "message"

	DocumentProcessing = "processing"
	DocumentOK         = "ok"
	DocumentError      = "error"
)

// DocumentRequest is one call to a /v1/documents/* endpoint: the request,
// what docpipe read from its files, and the response.
type DocumentRequest struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	Kind               string
	Status             string
	Request            map[string]any
	Result             any
	Response           any
	Error              string
	SessionID          *uuid.UUID
	UserMessageID      *uuid.UUID
	AssistantMessageID *uuid.UUID
	LatencyMS          int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Statuses of an uploaded Document (DocumentProcessing is shared with
// DocumentRequest).
const (
	DocumentPending = "pending"
	DocumentReady   = "ready"
	DocumentFailed  = "failed"
)

// Document is a file uploaded to /v1/documents and read in the background,
// page by page. Data is only loaded when the file is read.
type Document struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	Name              string
	MediaType         string
	Size              int64
	SHA256            string
	Data              []byte
	Status            string
	PagesTotal        int
	PagesDone         int
	FailedPages       int
	UnverifiedNumbers int
	TokenCount        int
	Error             string
	Attempts          int
	FinishedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// DocumentPage is what was read from one page of a Document.
type DocumentPage struct {
	DocumentID uuid.UUID
	PageNo     int
	Markdown   string
	OCRText    string
	Numbers    any // the number check (json), nil when it did not run
	Unverified int
	Error      string
	Tokens     int
}

// DocumentFile is one file uploaded with a DocumentRequest.
type DocumentFile struct {
	ID        uuid.UUID
	RequestID uuid.UUID
	Idx       int
	Name      string
	MediaType string
	Size      int64
	SHA256    string
	Data      []byte
}
