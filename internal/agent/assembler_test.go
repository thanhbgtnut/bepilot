package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/domain"
)

func idxPtr(i int) *int { return &i }

func TestAssemblerTextThenToolThenText(t *testing.T) {
	var kinds []events.Kind
	a := newAssembler(func(e events.Event) { kinds = append(kinds, e.Kind) })

	a.messageStart("msg_1", "m", 10)

	// model call 1: text, then a tool call streamed in fragments
	a.modelChunk(&schema.Message{Content: "Let me "})
	a.modelChunk(&schema.Message{Content: "check."})
	a.modelChunk(&schema.Message{ToolCalls: []schema.ToolCall{{
		Index: idxPtr(0), ID: "toolu_1", Function: schema.FunctionCall{Name: "load_skill", Arguments: `{"slug":`},
	}}})
	a.modelChunk(&schema.Message{ToolCalls: []schema.ToolCall{{
		Index: idxPtr(0), Function: schema.FunctionCall{Arguments: `"pdf-forms"}`},
	}}})
	a.modelCallDone("tool_calls", &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 5})

	// tool executes
	a.toolStart("load_skill", `{"slug":"pdf-forms"}`)
	a.toolEnd("load_skill", `{"body":"..."}`, false)

	// model call 2: final text
	a.modelChunk(&schema.Message{Content: "Here we go."})
	a.modelCallDone("stop", &schema.TokenUsage{PromptTokens: 150, CompletionTokens: 8})

	a.finish("end_turn")

	blocks, stats := a.result("end_turn")

	// blocks: text, tool_use, tool_result, text
	if len(blocks) != 4 {
		t.Fatalf("want 4 blocks, got %d: %+v", len(blocks), blocks)
	}
	wantTypes := []string{domain.BlockText, domain.BlockToolUse, domain.BlockToolResult, domain.BlockText}
	for i, want := range wantTypes {
		if blocks[i].Type != want {
			t.Fatalf("block %d: want %s got %s", i, want, blocks[i].Type)
		}
	}
	if blocks[0].Text != "Let me check." {
		t.Fatalf("text block 0: %q", blocks[0].Text)
	}
	if blocks[1].ToolName != "load_skill" || blocks[1].ToolUseID != "toolu_1" {
		t.Fatalf("tool_use block: %+v", blocks[1])
	}
	if got := blocks[1].ToolInput["slug"]; got != "pdf-forms" {
		t.Fatalf("tool_use input: %+v", blocks[1].ToolInput)
	}
	if blocks[2].ToolUseID != "toolu_1" || blocks[2].Text != `{"body":"..."}` {
		t.Fatalf("tool_result block: %+v", blocks[2])
	}
	if blocks[3].Text != "Here we go." {
		t.Fatalf("final text: %q", blocks[3].Text)
	}
	if stats.StopReason != "end_turn" {
		t.Fatalf("stop reason: %q", stats.StopReason)
	}
	if stats.OutputTokens != 13 {
		t.Fatalf("output tokens: %d", stats.OutputTokens)
	}
	if stats.InputTokens != 150 {
		t.Fatalf("input tokens: %d", stats.InputTokens)
	}
	if stats.Steps != 2 {
		t.Fatalf("steps: %d", stats.Steps)
	}

	// event ordering sanity
	assertOrder(t, kinds,
		events.KindMessageStart,
		events.KindContentBlockStart, // text
		events.KindTextDelta,
		events.KindContentBlockStop,
		events.KindContentBlockStart, // tool_use
		events.KindContentBlockStop,
		events.KindToolExecStart,
		events.KindToolExecStop,
		events.KindContentBlockStart, // final text
		events.KindContentBlockStop,
		events.KindMessageDelta,
		events.KindMessageStop,
	)
}

// assertOrder checks that want appears as a subsequence of got.
func assertOrder(t *testing.T, got []events.Kind, want ...events.Kind) {
	t.Helper()
	j := 0
	for _, k := range got {
		if j < len(want) && k == want[j] {
			j++
		}
	}
	if j != len(want) {
		t.Fatalf("event order mismatch; matched %d/%d\ngot: %v", j, len(want), got)
	}
}
