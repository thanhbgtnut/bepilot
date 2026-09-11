package events

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thanhenti/bepilot/internal/domain"
)

func aguiTypes(m *AGUIMapper, evs []Event) ([]string, []json.RawMessage) {
	var types []string
	var payloads []json.RawMessage
	for _, e := range evs {
		for _, f := range m.Map(e) {
			var p struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(f.Data, &p)
			types = append(types, p.Type)
			payloads = append(payloads, append(json.RawMessage(nil), f.Data...))
		}
	}
	return types, payloads
}

func TestAGUIMapperFullSequence(t *testing.T) {
	m := NewAGUIMapper("thread-1", "run-1")
	seq := []Event{
		{Kind: KindMessageStart, MessageID: "msg_1", Model: "claude-sonnet-5"},
		{Kind: KindContentBlockStart, Index: 0, BlockType: "text"},
		{Kind: KindTextDelta, Index: 0, Text: "Hel"},
		{Kind: KindTextDelta, Index: 0, Text: "lo"},
		{Kind: KindContentBlockStop, Index: 0},
		{Kind: KindContentBlockStart, Index: 1, BlockType: "tool_use", ToolUseID: "toolu_1", ToolName: "load_skill"},
		{Kind: KindInputJSONDelta, Index: 1, PartialJSON: `{"slug":"pdf-forms"}`},
		{Kind: KindContentBlockStop, Index: 1},
		{Kind: KindToolExecStart, ToolName: "load_skill", ToolUseID: "toolu_1"},
		{Kind: KindToolExecStop, ToolName: "load_skill", ToolUseID: "toolu_1", ToolResult: "ok"},
		{Kind: KindContentBlockStart, Index: 2, BlockType: "text"},
		{Kind: KindTextDelta, Index: 2, Text: "Done"},
		{Kind: KindContentBlockStop, Index: 2},
		{Kind: KindMessageDelta, StopReason: "end_turn", Usage: domain.Usage{OutputTokens: 7}},
		{Kind: KindMessageStop},
	}
	got, _ := aguiTypes(m, seq)
	want := []string{
		"RUN_STARTED",
		"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END",
		"TOOL_CALL_START", "TOOL_CALL_ARGS", "TOOL_CALL_END",
		"TOOL_CALL_RESULT",
		"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END",
		"RUN_FINISHED",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("frame types:\n got: %v\nwant: %v", got, want)
	}
	if !m.Started() || !m.Ended() {
		t.Fatalf("expected Started && Ended, got started=%v ended=%v", m.Started(), m.Ended())
	}
}

func TestAGUIMapperRunStartedShape(t *testing.T) {
	m := NewAGUIMapper("thread-x", "run-x")
	frames := m.Map(Event{Kind: KindMessageStart, MessageID: "msg_x"})
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	var p struct {
		Type     string `json:"type"`
		ThreadID string `json:"threadId"`
		RunID    string `json:"runId"`
	}
	if err := json.Unmarshal(frames[0].Data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != "RUN_STARTED" || p.ThreadID != "thread-x" || p.RunID != "run-x" {
		t.Fatalf("bad RUN_STARTED: %s", frames[0].Data)
	}
	if frames[0].Event != "" {
		t.Fatalf("AG-UI frames carry no event name, got %q", frames[0].Event)
	}
}

func TestAGUIMapperToolArgsAndResult(t *testing.T) {
	m := NewAGUIMapper("t", "r")
	_, payloads := aguiTypes(m, []Event{
		{Kind: KindMessageStart, MessageID: "msg_1"},
		{Kind: KindContentBlockStart, Index: 0, BlockType: "tool_use", ToolUseID: "toolu_9", ToolName: "web_search"},
		{Kind: KindInputJSONDelta, Index: 0, PartialJSON: `{"q":"x"}`},
		{Kind: KindContentBlockStop, Index: 0},
		{Kind: KindToolExecStop, ToolName: "web_search", ToolUseID: "toolu_9", ToolResult: "found"},
	})
	// payloads[0]=RUN_STARTED, [1]=TOOL_CALL_START, [2]=TOOL_CALL_ARGS, [3]=TOOL_CALL_END, [4]=TOOL_CALL_RESULT
	var args struct {
		ToolCallID string `json:"toolCallId"`
		Delta      string `json:"delta"`
	}
	_ = json.Unmarshal(payloads[2], &args)
	if args.ToolCallID != "toolu_9" || args.Delta != `{"q":"x"}` {
		t.Fatalf("bad TOOL_CALL_ARGS: %s", payloads[2])
	}
	var res struct {
		ToolCallID string `json:"toolCallId"`
		Content    string `json:"content"`
		Role       string `json:"role"`
	}
	_ = json.Unmarshal(payloads[4], &res)
	if res.ToolCallID != "toolu_9" || res.Content != "found" || res.Role != "tool" {
		t.Fatalf("bad TOOL_CALL_RESULT: %s", payloads[4])
	}
}

func TestAGUIMapperError(t *testing.T) {
	m := NewAGUIMapper("t", "r")
	_, payloads := aguiTypes(m, []Event{
		{Kind: KindMessageStart, MessageID: "msg_1"},
		{Kind: KindError, ErrType: "rate_limit_error", ErrMessage: "slow down"},
	})
	var p struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(payloads[len(payloads)-1], &p)
	if p.Type != "RUN_ERROR" || p.Message != "slow down" || p.Code != "rate_limit_error" {
		t.Fatalf("bad RUN_ERROR: %s", payloads[len(payloads)-1])
	}
	if !m.Ended() {
		t.Fatal("expected Ended after RUN_ERROR")
	}
}
