// Package dto holds the HTTP request/response types. The message types mirror
// the Anthropic Messages API so existing Claude clients work with minimal
// changes; bepilot-specific fields (provider, metadata.session_id) are additive.
package dto

import (
	"encoding/json"
	"strings"

	"github.com/thanhenti/bepilot/internal/domain"
)

// MessagesRequest is the body of POST /v1/messages.
type MessagesRequest struct {
	Model       string          `json:"model"`
	Provider    string          `json:"provider,omitempty"` // bepilot extension
	Messages    []InputMessage  `json:"messages"`
	System      json.RawMessage `json:"system,omitempty" swaggertype:"object"` // string, or [{"type":"text","text":"..."}]
	MaxTokens   int             `json:"max_tokens"`
	Temperature *float32        `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Metadata    *Metadata       `json:"metadata,omitempty"`

	// HistoryTokenBudget (bepilot extension) caps the approximate number of
	// tokens of conversation history sent to the model this turn. Oldest
	// messages are dropped first; the latest user message is always kept, even
	// if it alone exceeds the budget. 0 or omitted uses the server default
	// (agent.history_token_budget); a negative value is rejected.
	HistoryTokenBudget int `json:"history_token_budget,omitempty" example:"24000"`
}

// Metadata carries the optional user and session identifiers.
type Metadata struct {
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"` // bepilot extension
}

// InputMessage is one entry of the request messages array.
type InputMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content" swaggertype:"object"` // string, or an array of content blocks
}

// Text flattens the message content to plain text.
func (m InputMessage) Text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}

// SystemText flattens a request `system` field (string or block array).
func SystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}

// LastUserText returns the text of the final user message in the request.
func (r MessagesRequest) LastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].Text()
		}
	}
	return ""
}

// --- Responses ----------------------------------------------------------—-—-

// MessageResponse is the non-streaming response, shaped like Anthropic's.
type MessageResponse struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"` // "message"
	Role         string        `json:"role"` // "assistant"
	Model        string        `json:"model"`
	Content      []OutputBlock `json:"content"`
	StopReason   string        `json:"stop_reason"`
	StopSequence *string       `json:"stop_sequence"`
	Usage        Usage         `json:"usage"`
	Session      *SessionBrief `json:"session,omitempty"` // bepilot extension
}

// SteeredResponse is returned instead of a message when the session already had
// a turn running and the new message was handed to it. The reply is not in this
// response: it arrives on the stream of the turn that is running.
type SteeredResponse struct {
	Type      string        `json:"type"`       // "steered"
	MessageID string        `json:"message_id"` // the stored user message
	Session   *SessionBrief `json:"session,omitempty"`
}

// OutputBlock is one content block in a response.
type OutputBlock struct {
	Type      string         `json:"type"` // text | tool_use | tool_result | thinking
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// Usage is the token accounting block.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// BlocksToOutput converts stored content blocks to response blocks.
func BlocksToOutput(blocks []domain.ContentBlock) []OutputBlock {
	out := make([]OutputBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case domain.BlockText:
			out = append(out, OutputBlock{Type: "text", Text: b.Text})
		case domain.BlockThinking:
			out = append(out, OutputBlock{Type: "thinking", Thinking: b.Thinking})
		case domain.BlockToolUse:
			out = append(out, OutputBlock{Type: "tool_use", ID: b.ToolUseID, Name: b.ToolName, Input: b.ToolInput})
		case domain.BlockToolResult:
			content := b.ToolResult
			if content == nil {
				content = b.Text
			}
			out = append(out, OutputBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Content: content, IsError: b.IsError})
		}
	}
	return out
}

// ErrorResponse is the Anthropic-style error envelope.
type ErrorResponse struct {
	Type  string `json:"type"` // "error"
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewError builds an ErrorResponse.
func NewError(kind, msg string) ErrorResponse {
	var e ErrorResponse
	e.Type = "error"
	e.Error.Type = kind
	e.Error.Message = msg
	return e
}
