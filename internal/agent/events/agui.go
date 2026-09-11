package events

import (
	"encoding/json"
	"strconv"

	"github.com/hertz-contrib/sse"
)

// AGUIMapper converts the internal agent event stream into AG-UI protocol SSE
// frames, so an AG-UI client (e.g. CopilotKit's HttpAgent) can drive a bepilot
// turn directly. It is the sibling of AnthropicMapper: one internal stream,
// two wire formats.
//
// AG-UI frames are plain `data: {json}` lines whose JSON carries a `type`
// discriminator (RUN_STARTED, TEXT_MESSAGE_START, TEXT_MESSAGE_CONTENT,
// TOOL_CALL_START, …). See https://docs.ag-ui.com/concepts/events.
//
// The mapper is stateful: it remembers the run identifiers, the assistant
// message id, and the type + tool id of every open content block so it can
// emit the matching *_END event. It also emits RUN_STARTED lazily before the
// first mapped event and records whether a terminal RUN_FINISHED / RUN_ERROR
// has gone out, which lets the handler close a partial stream cleanly.
//
// Thinking / reasoning deltas are intentionally dropped: AG-UI clients that do
// not model reasoning events reject an unknown `type`, and the buffered
// (non-AG-UI) response still carries the thinking blocks.
type AGUIMapper struct {
	threadID string
	runID    string

	msgID     string         // assistant message id, from message_start
	blockType map[int]string // sse block index -> "text" | "thinking" | "tool_use"
	toolID    map[int]string // sse block index -> tool call id (for tool_use blocks)
	lastText  string         // most recent text message id, used as parentMessageId

	started bool
	ended   bool
}

// NewAGUIMapper creates a mapper bound to one AG-UI run. threadID is the
// bepilot session id; runID identifies this turn.
func NewAGUIMapper(threadID, runID string) *AGUIMapper {
	return &AGUIMapper{
		threadID:  threadID,
		runID:     runID,
		blockType: map[int]string{},
		toolID:    map[int]string{},
	}
}

// Started reports whether RUN_STARTED has been emitted.
func (m *AGUIMapper) Started() bool { return m.started }

// Ended reports whether a terminal RUN_FINISHED or RUN_ERROR has been emitted.
func (m *AGUIMapper) Ended() bool { return m.ended }

// Map returns zero or more AG-UI SSE frames for one internal event.
func (m *AGUIMapper) Map(ev Event) []*sse.Event {
	var out []*sse.Event
	if !m.started {
		m.started = true
		out = append(out, AGUIFrame(map[string]any{
			"type": "RUN_STARTED", "threadId": m.threadID, "runId": m.runID,
		}))
	}
	return append(out, m.mapOne(ev)...)
}

func (m *AGUIMapper) mapOne(ev Event) []*sse.Event {
	switch ev.Kind {
	case KindMessageStart:
		m.msgID = ev.MessageID
		return nil

	case KindContentBlockStart:
		m.blockType[ev.Index] = ev.BlockType
		switch ev.BlockType {
		case "text":
			id := m.textID(ev.Index)
			m.lastText = id
			return frames(map[string]any{
				"type": "TEXT_MESSAGE_START", "messageId": id, "role": "assistant",
			})
		case "tool_use":
			m.toolID[ev.Index] = ev.ToolUseID
			parent := m.lastText
			if parent == "" {
				parent = m.msgID
			}
			return frames(map[string]any{
				"type": "TOOL_CALL_START", "toolCallId": ev.ToolUseID,
				"toolCallName": ev.ToolName, "parentMessageId": parent,
			})
		}
		return nil

	case KindTextDelta:
		return frames(map[string]any{
			"type": "TEXT_MESSAGE_CONTENT", "messageId": m.textID(ev.Index), "delta": ev.Text,
		})

	case KindInputJSONDelta:
		return frames(map[string]any{
			"type": "TOOL_CALL_ARGS", "toolCallId": m.toolID[ev.Index], "delta": ev.PartialJSON,
		})

	case KindContentBlockStop:
		switch m.blockType[ev.Index] {
		case "text":
			return frames(map[string]any{"type": "TEXT_MESSAGE_END", "messageId": m.textID(ev.Index)})
		case "tool_use":
			return frames(map[string]any{"type": "TOOL_CALL_END", "toolCallId": m.toolID[ev.Index]})
		}
		return nil

	case KindToolExecStop:
		return frames(map[string]any{
			"type":       "TOOL_CALL_RESULT",
			"messageId":  ev.ToolUseID + "_result",
			"toolCallId": ev.ToolUseID,
			"content":    stringifyResult(ev.ToolResult),
			"role":       "tool",
		})

	case KindMessageStop:
		m.ended = true
		return frames(map[string]any{
			"type": "RUN_FINISHED", "threadId": m.threadID, "runId": m.runID,
		})

	case KindError:
		m.ended = true
		return frames(map[string]any{
			"type": "RUN_ERROR", "message": ev.ErrMessage, "code": orDefault(ev.ErrType, "api_error"),
		})
	}

	// KindThinkingDelta, KindToolExecStart, KindMessageDelta: no AG-UI frame.
	return nil
}

func (m *AGUIMapper) textID(index int) string {
	return m.msgID + "-" + strconv.Itoa(index)
}

// AGUIFrame marshals an AG-UI event payload into a single SSE frame. AG-UI
// frames have no `event:` name — the type lives in the JSON `type` field.
func AGUIFrame(payload any) *sse.Event {
	data, _ := json.Marshal(payload)
	return &sse.Event{Data: data}
}

func frames(payload any) []*sse.Event { return []*sse.Event{AGUIFrame(payload)} }

// stringifyResult coerces a tool result into the string AG-UI's
// TOOL_CALL_RESULT.content expects.
func stringifyResult(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
