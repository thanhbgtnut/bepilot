package events

import (
	"encoding/json"

	"github.com/hertz-contrib/sse"
)

// AnthropicMapper converts internal events into Anthropic-compatible SSE
// frames. It is stateful: it tracks the message id, model and running output
// token count so message_start / message_delta carry the right shape.
//
// Standard Anthropic event types are emitted verbatim (message_start,
// content_block_start, content_block_delta, content_block_stop, message_delta,
// message_stop, error). Tool execution progress is emitted as the bepilot
// extension events tool_execution_start / tool_execution_stop, which standard
// Anthropic clients ignore.
type AnthropicMapper struct {
	messageID string
	model     string
	inputToks int
}

// NewAnthropicMapper creates a mapper. inputTokens seeds the usage reported in
// message_start.
func NewAnthropicMapper(inputTokens int) *AnthropicMapper {
	return &AnthropicMapper{inputToks: inputTokens}
}

// Map returns zero or more SSE frames for one internal event.
func (m *AnthropicMapper) Map(ev Event) []*sse.Event {
	switch ev.Kind {
	case KindMessageStart:
		m.messageID = ev.MessageID
		m.model = ev.Model
		return m.frame("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": ev.MessageID,
				// session_id is a bepilot extension (standard Anthropic clients
				// ignore unknown fields): the only place a streaming /v1/messages
				// call surfaces which session a new turn landed in, since — unlike
				// the buffered response's `session` field — nothing else in this
				// event stream carries it.
				"session_id":    ev.SessionID,
				"type":          "message",
				"role":          "assistant",
				"model":         ev.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  m.inputToks,
					"output_tokens": 0,
				},
			},
		})

	case KindContentBlockStart:
		block := map[string]any{"type": ev.BlockType}
		switch ev.BlockType {
		case "text":
			block["text"] = ""
		case "thinking":
			block["thinking"] = ""
		case "tool_use":
			block["id"] = ev.ToolUseID
			block["name"] = ev.ToolName
			block["input"] = map[string]any{}
		}
		return m.frame("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         ev.Index,
			"content_block": block,
		})

	case KindTextDelta:
		return m.frame("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": ev.Index,
			"delta": map[string]any{"type": "text_delta", "text": ev.Text},
		})

	case KindThinkingDelta:
		return m.frame("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": ev.Index,
			"delta": map[string]any{"type": "thinking_delta", "thinking": ev.Text},
		})

	case KindInputJSONDelta:
		return m.frame("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": ev.Index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": ev.PartialJSON},
		})

	case KindContentBlockStop:
		return m.frame("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": ev.Index,
		})

	case KindToolExecStart:
		return m.frame("tool_execution_start", map[string]any{
			"type":        "tool_execution_start",
			"tool_use_id": ev.ToolUseID,
			"name":        ev.ToolName,
			"input":       jsonRawOrNull(ev.ToolInput),
		})

	case KindToolExecStop:
		return m.frame("tool_execution_stop", map[string]any{
			"type":        "tool_execution_stop",
			"tool_use_id": ev.ToolUseID,
			"name":        ev.ToolName,
			"is_error":    ev.IsError,
			"result":      ev.ToolResult,
		})

	case KindMessageDelta:
		return m.frame("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   ev.StopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{"output_tokens": ev.Usage.OutputTokens},
		})

	case KindMessageStop:
		return m.frame("message_stop", map[string]any{"type": "message_stop"})

	case KindError:
		return m.frame("error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    orDefault(ev.ErrType, "api_error"),
				"message": ev.ErrMessage,
			},
		})
	}
	return nil
}

func (m *AnthropicMapper) frame(event string, payload any) []*sse.Event {
	data, _ := json.Marshal(payload)
	return []*sse.Event{{Event: event, Data: data}}
}

func jsonRawOrNull(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(r, &v); err != nil {
		return string(r)
	}
	return v
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
