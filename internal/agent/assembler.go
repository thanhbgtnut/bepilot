package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/domain"
)

// assembler turns the raw signals of a react run (model output chunks, model
// call boundaries, tool start/end) into an ordered stream of events.Event and,
// in parallel, accumulates the final assistant content blocks for persistence.
//
// All exported-ish methods are guarded by a mutex because model-stream
// consumption and tool callbacks may originate on different goroutines.
type assembler struct {
	mu   sync.Mutex
	emit events.Sink

	sseIndex int // next content-block index for SSE-visible blocks (text/thinking/tool_use)
	order    int // monotonic ordering for every block, incl. tool_result
	open     *openBlock

	blocks       []domain.ContentBlock
	pendingTools []*toolRef

	outputTokens int
	inputTokens  int
	lastFinish   string
	steps        int
	failed       bool
	failErrType  string
	failErrMsg   string
}

type openBlock struct {
	index     int
	typ       string // text | thinking | tool_use
	streamIdx int
	toolID    string
	toolName  string
	text      strings.Builder
	args      strings.Builder
}

type toolRef struct {
	id      string
	name    string
	input   json.RawMessage
	started bool
	done    bool
}

func newAssembler(emit events.Sink) *assembler {
	if emit == nil {
		emit = func(events.Event) {}
	}
	return &assembler{emit: emit}
}

func (a *assembler) messageStart(messageID, model, sessionID string, inputTokens int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inputTokens = inputTokens
	a.emit(events.Event{Kind: events.KindMessageStart, MessageID: messageID, Model: model, SessionID: sessionID, Usage: domain.Usage{InputTokens: inputTokens}})
}

// modelChunk handles one streamed message chunk from a model call.
func (a *assembler) modelChunk(m *schema.Message) {
	if m == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if m.ReasoningContent != "" {
		a.ensureLocked("thinking")
		a.open.text.WriteString(m.ReasoningContent)
		a.emit(events.Event{Kind: events.KindThinkingDelta, Index: a.open.index, Text: m.ReasoningContent})
	}
	if m.Content != "" {
		a.ensureLocked("text")
		a.open.text.WriteString(m.Content)
		a.emit(events.Event{Kind: events.KindTextDelta, Index: a.open.index, Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		if a.open == nil || a.open.typ != "tool_use" || a.open.streamIdx != idx {
			a.closeLocked()
			a.open = &openBlock{index: a.nextSSE(), typ: "tool_use", streamIdx: idx, toolID: tc.ID, toolName: tc.Function.Name}
			a.emit(events.Event{
				Kind: events.KindContentBlockStart, Index: a.open.index,
				BlockType: "tool_use", ToolUseID: a.open.toolID, ToolName: a.open.toolName,
			})
		}
		if tc.ID != "" && a.open.toolID == "" {
			a.open.toolID = tc.ID
		}
		if tc.Function.Name != "" && a.open.toolName == "" {
			a.open.toolName = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			a.open.args.WriteString(tc.Function.Arguments)
			a.emit(events.Event{Kind: events.KindInputJSONDelta, Index: a.open.index, PartialJSON: tc.Function.Arguments})
		}
	}
}

// modelCallDone closes any open block from the just-finished model call.
func (a *assembler) modelCallDone(finishReason string, usage *schema.TokenUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steps++
	a.closeLocked()
	if finishReason != "" {
		a.lastFinish = finishReason
	}
	if usage != nil {
		if usage.PromptTokens > a.inputTokens {
			a.inputTokens = usage.PromptTokens
		}
		a.outputTokens += usage.CompletionTokens
	}
}

func (a *assembler) toolStart(name, argsJSON string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ref := a.matchPending(name, argsJSON)
	if ref == nil {
		ref = &toolRef{name: name, input: json.RawMessage(argsJSON)}
		a.pendingTools = append(a.pendingTools, ref)
	}
	ref.started = true
	a.emit(events.Event{
		Kind: events.KindToolExecStart, ToolName: ref.name, ToolUseID: ref.id,
		ToolInput: ref.input,
	})
}

func (a *assembler) toolEnd(name, response string, isErr bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ref := a.matchStarted(name)
	toolUseID := ""
	if ref != nil {
		toolUseID = ref.id
		ref.done = true
	}
	a.blocks = append(a.blocks, domain.ContentBlock{
		Idx:       a.nextOrder(),
		Type:      domain.BlockToolResult,
		Text:      response,
		ToolName:  name,
		ToolUseID: toolUseID,
		IsError:   isErr,
	})
	a.emit(events.Event{
		Kind: events.KindToolExecStop, ToolName: name, ToolUseID: toolUseID,
		ToolResult: response, IsError: isErr,
	})
}

// finish closes any open block and resolves the run's final stop reason.
// It deliberately does NOT emit the terminal SSE event (that's emitStop) —
// see emitStop's doc for why the two are split.
func (a *assembler) finish(defaultFinish string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeLocked()
	reason := mapFinishReason(a.lastFinish)
	if reason == "" {
		reason = defaultFinish
	}
	a.lastFinish = reason
}

// fail closes any open block and marks the run as failed. Like finish, it
// does NOT emit the terminal SSE event — see emitStop.
func (a *assembler) fail(errType, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failed = true
	a.failErrType = errType
	a.failErrMsg = msg
	a.closeLocked()
}

// emitStop emits the terminal SSE event for this turn: KindMessageStop
// (AG-UI RUN_FINISHED) on success, or KindError (AG-UI RUN_ERROR) if fail()
// was called.
//
// Callers MUST persist the assistant message (via result(), which reads the
// same blocks finish()/fail() just closed) BEFORE calling this. An AG-UI
// client legitimately treats RUN_FINISHED/RUN_ERROR as "the turn is done,
// safe to read back" and may immediately re-fetch the session over REST to
// restore or verify its view (bepilot's own frontend does exactly this).
// Emitting the terminal event before the message.Append transaction commits
// lets that re-fetch land in the gap and see the turn as if it never
// happened. This is not hypothetical: it was reproduced live — a turn with
// many tool calls took long enough to persist (one INSERT per content
// block) that a client re-fetch right after RUN_FINISHED consistently saw
// only the user's message, wiping the assistant's reply from the UI.
func (a *assembler) emitStop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		a.emit(events.Event{Kind: events.KindError, ErrType: a.failErrType, ErrMessage: a.failErrMsg})
		return
	}
	a.emit(events.Event{
		Kind: events.KindMessageDelta, StopReason: a.lastFinish,
		Usage: domain.Usage{InputTokens: a.inputTokens, OutputTokens: a.outputTokens},
	})
	a.emit(events.Event{Kind: events.KindMessageStop})
}

// --- locked helpers -------------------------------------------------------—-

// nextSSE returns the next content-block index visible in the SSE stream.
func (a *assembler) nextSSE() int {
	i := a.sseIndex
	a.sseIndex++
	return i
}

// nextOrder returns the next monotonic ordering key for a persisted block.
func (a *assembler) nextOrder() int {
	i := a.order
	a.order++
	return i
}

func (a *assembler) ensureLocked(typ string) {
	if a.open != nil && a.open.typ == typ {
		return
	}
	a.closeLocked()
	a.open = &openBlock{index: a.nextSSE(), typ: typ}
	a.emit(events.Event{Kind: events.KindContentBlockStart, Index: a.open.index, BlockType: typ})
}

func (a *assembler) closeLocked() {
	ob := a.open
	if ob == nil {
		return
	}
	a.open = nil
	order := a.nextOrder()

	switch ob.typ {
	case "text":
		a.blocks = append(a.blocks, domain.ContentBlock{Idx: order, Type: domain.BlockText, Text: ob.text.String()})
	case "thinking":
		a.blocks = append(a.blocks, domain.ContentBlock{Idx: order, Type: domain.BlockThinking, Thinking: ob.text.String()})
	case "tool_use":
		input := parseToolArgs(ob.args.String())
		a.blocks = append(a.blocks, domain.ContentBlock{
			Idx: order, Type: domain.BlockToolUse,
			ToolName: ob.toolName, ToolUseID: ob.toolID, ToolInput: input,
		})
		raw, _ := json.Marshal(input)
		a.pendingTools = append(a.pendingTools, &toolRef{id: ob.toolID, name: ob.toolName, input: raw})
	}
	a.emit(events.Event{Kind: events.KindContentBlockStop, Index: ob.index})
}

func (a *assembler) matchPending(name, argsJSON string) *toolRef {
	norm := normalizeJSON(argsJSON)
	for _, r := range a.pendingTools {
		if r.name == name && !r.started && (norm == "" || normalizeJSON(string(r.input)) == norm) {
			return r
		}
	}
	for _, r := range a.pendingTools {
		if r.name == name && !r.started {
			return r
		}
	}
	return nil
}

func (a *assembler) matchStarted(name string) *toolRef {
	for _, r := range a.pendingTools {
		if r.name == name && r.started && !r.done {
			return r
		}
	}
	return nil
}

// result returns the accumulated blocks in index order plus run stats.
func (a *assembler) result(defaultFinish string) ([]domain.ContentBlock, runStats) {
	a.mu.Lock()
	defer a.mu.Unlock()
	blocks := make([]domain.ContentBlock, len(a.blocks))
	copy(blocks, a.blocks)
	sortBlocksByIdx(blocks)
	reason := mapFinishReason(a.lastFinish)
	if a.failed {
		reason = "error"
	} else if reason == "" {
		reason = defaultFinish
	}
	return blocks, runStats{
		StopReason:   reason,
		InputTokens:  a.inputTokens,
		OutputTokens: a.outputTokens,
		Steps:        a.steps,
	}
}

type runStats struct {
	StopReason   string
	InputTokens  int
	OutputTokens int
	Steps        int
}

func sortBlocksByIdx(b []domain.ContentBlock) {
	for i := 1; i < len(b); i++ {
		for j := i; j > 0 && b[j-1].Idx > b[j].Idx; j-- {
			b[j-1], b[j] = b[j], b[j-1]
		}
	}
	for i := range b {
		b[i].Idx = i
	}
}

func parseToolArgs(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{"_raw": s}
	}
	return m
}

func normalizeJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func mapFinishReason(r string) string {
	switch strings.ToLower(r) {
	case "stop", "end_turn", "":
		if r == "" {
			return ""
		}
		return "end_turn"
	case "length", "max_tokens":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "end_turn" // the loop resolved the tool calls
	case "content_filter":
		return "stop_sequence"
	default:
		return r
	}
}
