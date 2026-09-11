// Package events defines the transport-agnostic event stream emitted while an
// agent turn runs, plus the mapping to Anthropic-style SSE frames.
package events

import (
	"encoding/json"

	"github.com/thanhenti/bepilot/internal/domain"
)

// Kind enumerates the internal event types.
type Kind string

const (
	KindMessageStart      Kind = "message_start"
	KindContentBlockStart Kind = "content_block_start"
	KindTextDelta         Kind = "text_delta"
	KindThinkingDelta     Kind = "thinking_delta"
	KindInputJSONDelta    Kind = "input_json_delta"
	KindContentBlockStop  Kind = "content_block_stop"
	KindToolExecStart     Kind = "tool_execution_start" // bepilot extension
	KindToolExecStop      Kind = "tool_execution_stop"  // bepilot extension
	KindMessageDelta      Kind = "message_delta"
	KindMessageStop       Kind = "message_stop"
	KindError             Kind = "error"
)

// Event is one step in an agent turn.
type Event struct {
	Kind Kind

	// MessageStart
	MessageID string
	Model     string

	// Content block framing
	Index     int
	BlockType string // "text" | "thinking" | "tool_use"

	// Deltas
	Text        string // TextDelta / ThinkingDelta
	PartialJSON string // InputJSONDelta

	// tool_use block + tool execution
	ToolName   string
	ToolUseID  string
	ToolInput  json.RawMessage
	ToolResult any
	IsError    bool

	// MessageDelta / MessageStop
	StopReason string
	Usage      domain.Usage

	// Error
	ErrType    string
	ErrMessage string
}

// Sink receives events as a turn runs. Implementations must be safe for
// sequential calls from a single goroutine.
type Sink func(Event)
