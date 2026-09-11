package events

import (
	"encoding/json"
	"testing"

	"github.com/thanhenti/bepilot/internal/domain"
)

func mapAll(m *AnthropicMapper, evs []Event) []string {
	var kinds []string
	for _, e := range evs {
		for _, f := range m.Map(e) {
			kinds = append(kinds, f.Event)
		}
	}
	return kinds
}

func TestMapperEmitsAnthropicSequence(t *testing.T) {
	m := NewAnthropicMapper(42)
	seq := []Event{
		{Kind: KindMessageStart, MessageID: "msg_1", Model: "claude-sonnet-5"},
		{Kind: KindContentBlockStart, Index: 0, BlockType: "text"},
		{Kind: KindTextDelta, Index: 0, Text: "Hel"},
		{Kind: KindTextDelta, Index: 0, Text: "lo"},
		{Kind: KindContentBlockStop, Index: 0},
		{Kind: KindContentBlockStart, Index: 1, BlockType: "tool_use", ToolUseID: "toolu_1", ToolName: "load_skill"},
		{Kind: KindInputJSONDelta, Index: 1, PartialJSON: `{"slug":"pdf-forms"}`},
		{Kind: KindContentBlockStop, Index: 1},
		{Kind: KindToolExecStart, ToolName: "load_skill", ToolUseID: "toolu_1", ToolInput: json.RawMessage(`{"slug":"pdf-forms"}`)},
		{Kind: KindToolExecStop, ToolName: "load_skill", ToolUseID: "toolu_1", ToolResult: "ok"},
		{Kind: KindContentBlockStart, Index: 2, BlockType: "text"},
		{Kind: KindTextDelta, Index: 2, Text: "Done"},
		{Kind: KindContentBlockStop, Index: 2},
		{Kind: KindMessageDelta, StopReason: "end_turn", Usage: domain.Usage{OutputTokens: 7}},
		{Kind: KindMessageStop},
	}
	got := mapAll(m, seq)
	want := []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"tool_execution_start", "tool_execution_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}
	if len(got) != len(want) {
		t.Fatalf("frame count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d: got %q want %q\nfull: %v", i, got[i], want[i], got)
		}
	}
}

func TestMapperMessageStartShape(t *testing.T) {
	m := NewAnthropicMapper(42)
	frames := m.Map(Event{Kind: KindMessageStart, MessageID: "msg_x", Model: "m"})
	var payload struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Role  string `json:"role"`
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(frames[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "message_start" || payload.Message.ID != "msg_x" || payload.Message.Role != "assistant" || payload.Message.Usage.InputTokens != 42 {
		t.Fatalf("bad message_start payload: %s", frames[0].Data)
	}
}

func TestMapperTextDeltaShape(t *testing.T) {
	m := NewAnthropicMapper(0)
	frames := m.Map(Event{Kind: KindTextDelta, Index: 3, Text: "hi"})
	var p struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	_ = json.Unmarshal(frames[0].Data, &p)
	if p.Type != "content_block_delta" || p.Index != 3 || p.Delta.Type != "text_delta" || p.Delta.Text != "hi" {
		t.Fatalf("bad delta payload: %s", frames[0].Data)
	}
}
