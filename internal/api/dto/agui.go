package dto

import (
	"encoding/json"
	"strings"
)

// AGUIRunAgentInput is the body of POST /v1/ag-ui/run. It follows the AG-UI
// protocol's RunAgentInput shape (https://docs.ag-ui.com/sdk/js/core/types) so
// an AG-UI client can talk to bepilot without a bridge.
//
// `state` is folded into the system prompt as read-only client state, and
// `context` items are folded in as situational notes. `tools` are bound to
// the turn as client-executed tools: the agent may call them, but bepilot
// only forwards the call as AG-UI `TOOL_CALL_*` frames and ends the run
// immediately after — the client must execute the tool itself and send the
// result back as a `tool` message on the next run. `forwardedProps` has no
// defined bepilot semantics and remains accepted-but-ignored.
type AGUIRunAgentInput struct {
	ThreadID       string               `json:"threadId"`
	RunID          string               `json:"runId,omitempty"`
	Messages       []AGUIMessage        `json:"messages"`
	State          json.RawMessage      `json:"state,omitempty" swaggertype:"object"`
	Tools          []AGUIToolDefinition `json:"tools,omitempty"`
	Context        []AGUIContextItem    `json:"context,omitempty"`
	ForwardedProps json.RawMessage      `json:"forwardedProps,omitempty" swaggertype:"object"`
}

// AGUIContextItem is one entry of AG-UI's `context` array: a labeled piece of
// situational information the client wants the agent aware of for this turn
// (e.g. the current page, a selection, a user profile field).
type AGUIContextItem struct {
	Description string `json:"description"`
	Value       string `json:"value"`
}

// AGUIToolDefinition is one entry of AG-UI's `tools` array: a tool the
// client itself implements and executes. `Parameters` is a JSON Schema object
// describing the tool's arguments (may be omitted for a no-argument tool).
type AGUIToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty" swaggertype:"object"`
}

// AGUIMessage is one entry of the AG-UI messages array. `content` is a string
// for user/system/tool messages and may be omitted on assistant tool-call
// messages.
type AGUIMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"` // user | assistant | tool | system | developer
	Content    json.RawMessage `json:"content,omitempty" swaggertype:"string"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
}

// Text flattens the message content (a string, or an array of
// {type,text} parts) to plain text.
func (m AGUIMessage) Text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// LastUserText returns the text of the final user message in the request.
func (r AGUIRunAgentInput) LastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].Text()
		}
	}
	return ""
}
