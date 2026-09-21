package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/tools"
)

func TestSteererSplicesMessagesInOrderAndKeepsThemThere(t *testing.T) {
	s := &steerer{}
	u := func(c string) *schema.Message { return schema.UserMessage(c) }

	in1 := []*schema.Message{u("sys"), u("q"), u("tool-result-1")}
	if got := s.apply(in1); len(got) != 3 {
		t.Fatalf("nothing queued: transcript must be untouched, got %d", len(got))
	}

	if !s.push(steerMsg{Text: "change of plan"}) {
		t.Fatal("push refused on an open inbox")
	}
	got := s.apply(in1)
	if len(got) != 4 || !strings.Contains(got[3].Content, "change of plan") || !strings.HasPrefix(got[3].Content, steerNotice) {
		t.Fatalf("message must follow the transcript it arrived after: %+v", got)
	}
	if len(in1) != 3 {
		t.Fatal("apply must not mutate its input")
	}

	// Later steps append after the message; it must stay where the model first saw it.
	in2 := append(append([]*schema.Message{}, in1...), u("assistant-2"), u("tool-result-2"))
	got = s.apply(in2)
	if len(got) != 6 || !strings.Contains(got[3].Content, "change of plan") || got[4].Content != "assistant-2" {
		t.Fatalf("message moved: %+v", got)
	}
}

func TestSteererCloseHandsBackWhatWasNeverRead(t *testing.T) {
	s := &steerer{}
	s.push(steerMsg{Text: "read"})
	s.apply([]*schema.Message{schema.UserMessage("q")})
	s.push(steerMsg{Text: "late"})

	left := s.close()
	if len(left) != 1 || left[0].Text != "late" {
		t.Fatalf("left = %+v, want only the message the model never saw", left)
	}
	if s.push(steerMsg{Text: "after close"}) {
		t.Fatal("a closed inbox must refuse messages so the sender starts its own turn")
	}
	if again := s.close(); len(again) != 0 {
		t.Fatalf("second close returned %+v", again)
	}
}

func TestIsStopRequest(t *testing.T) {
	for _, text := range []string{"stop", "Stop!", "dừng", "Dừng lại", "dừng lại đi", "hủy", "cancel that", "Huỷ."} {
		if !isStopRequest(text) {
			t.Errorf("%q should be a stop request", text)
		}
	}
	for _, text := range []string{"", "hủy hợp đồng số 3 giúp tôi", "please stop", "thôi làm tiếp đi", "stop using tables in the answer", "so sánh hai file"} {
		if isStopRequest(text) {
			t.Errorf("%q must not be a stop request", text)
		}
	}
}

func TestPairToolCallsDropsHalfFinishedCalls(t *testing.T) {
	i0, i1 := 0, 1
	calls := []schema.ToolCall{
		{ID: "a", Index: &i0, Function: schema.FunctionCall{Name: "calculate"}},
		{ID: "b", Index: &i1, Function: schema.FunctionCall{Name: "http_fetch"}}, // interrupted before it returned
	}
	results := []*schema.Message{{Role: schema.Tool, ToolCallID: "a"}, {Role: schema.Tool, ToolCallID: "orphan"}}
	gotCalls, gotResults := pairToolCalls(calls, results)
	if len(gotCalls) != 1 || gotCalls[0].ID != "a" || len(gotResults) != 1 || gotResults[0].ToolCallID != "a" {
		t.Fatalf("calls=%+v results=%+v", gotCalls, gotResults)
	}
}

func TestRunRegistryAllowsOneTurnPerSession(t *testing.T) {
	g := newRunRegistry()
	id := uuid.New()
	a, owner := g.acquire(id, func() {})
	if !owner {
		t.Fatal("first acquire must own the session")
	}
	b, owner := g.acquire(id, func() {})
	if owner || b != a {
		t.Fatal("second acquire must see the running turn, not start another")
	}
	if _, owner := g.acquire(uuid.New(), func() {}); !owner {
		t.Fatal("other sessions are independent")
	}
	g.release(id, a)
	select {
	case <-a.done:
	default:
		t.Fatal("release must wake waiters")
	}
	if _, owner := g.acquire(id, func() {}); !owner {
		t.Fatal("session must be free after release")
	}
}

// steerProbe is a model that works through tools for a few steps and records
// every transcript it is shown. Once it sees the user's new message it answers.
type steerProbe struct {
	mu         *sync.Mutex
	transcript *[][]string
	bound      bool
	onStep     func(n int) // called before the n-th call returns, from the model goroutine
}

func (m *steerProbe) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.bound = len(infos) > 0
	return &cp, nil
}

func (m *steerProbe) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	var seen []string
	steered := false
	for _, msg := range in {
		seen = append(seen, string(msg.Role)+": "+msg.Content)
		if strings.Contains(msg.Content, "use USD") {
			steered = true
		}
	}
	m.mu.Lock()
	// It reacts to the message with one more tool round before it answers, so
	// the test can see the message on a step that is not the first to read it.
	reacted := false
	for _, prev := range *m.transcript {
		if strings.Contains(strings.Join(prev, "\n"), "use USD") {
			reacted = true
		}
	}
	*m.transcript = append(*m.transcript, seen)
	n := len(*m.transcript)
	m.mu.Unlock()
	if m.onStep != nil {
		m.onStep(n)
	}
	if (steered && reacted) || !m.bound {
		return &schema.Message{Role: schema.Assistant, Content: "answered with the new instruction"}, nil
	}
	idx := 0
	args, _ := json.Marshal(map[string]any{"timezone": "UTC"})
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		Index: &idx, ID: "call_" + string(rune('a'+n)), Type: "function",
		Function: schema.FunctionCall{Name: "current_time", Arguments: string(args)},
	}}}, nil
}

func (m *steerProbe) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, o...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func TestMessageSentMidTurnReachesTheModelBeforeItsNextStep(t *testing.T) {
	reg, err := tools.NewRegistry(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := reg.NewSession()
	ctx := tools.WithSession(context.Background(), sess)

	inbox := &steerer{}
	var (
		mu         sync.Mutex
		transcript [][]string
	)
	cm := sess.WrapModel(&steerProbe{
		mu: &mu, transcript: &transcript,
		// The user writes while the model is busy with its second step.
		onStep: func(n int) {
			if n == 2 {
				inbox.push(steerMsg{Text: "use USD instead"})
			}
		},
	})
	ra, err := buildReactAgent(ctx, cm, sess.Executable(), "sys", 16, nil, inbox)
	if err != nil {
		t.Fatal(err)
	}

	var evs []events.Event
	asm := newAssembler(func(e events.Event) { evs = append(evs, e) })
	history := []*einoMessage{{Role: schema.User, Content: "summarise the report"}}
	if err := (&Agent{}).drive(ctx, ra, history, newCallbackHandler(asm)); err != nil {
		t.Fatal(err)
	}

	if len(transcript) != 4 {
		t.Fatalf("model calls = %d, want 4 (two tool rounds, the step that read the message, and the answer)", len(transcript))
	}
	for i, seen := range transcript[:2] {
		if strings.Contains(strings.Join(seen, "\n"), "use USD") {
			t.Fatalf("call %d saw the message before it was sent", i+1)
		}
	}
	third := strings.Join(transcript[2], "\n")
	if !strings.Contains(third, "use USD instead") {
		t.Fatalf("the step after the message was sent must see it:\n%s", third)
	}
	// It is part of the conversation from then on, in the same place.
	if !strings.Contains(strings.Join(transcript[3], "\n"), "use USD instead") {
		t.Fatal("message dropped from later steps")
	}
	blocks, _ := asm.result("end_turn")
	var answer string
	for _, b := range blocks {
		if b.Type == domain.BlockText {
			answer += b.Text
		}
	}
	if answer != "answered with the new instruction" {
		t.Errorf("answer = %q", answer)
	}
}

func TestInterruptEndsTheTurnCleanly(t *testing.T) {
	asm := newAssembler(nil)
	asm.modelChunk(&schema.Message{Role: schema.Assistant, Content: "partial answ"})
	asm.fail("api_error", "context canceled") // what the cancelled model call reports
	asm.interrupt()
	blocks, stats := asm.result("end_turn")
	if stats.StopReason != "interrupted" {
		t.Fatalf("stop reason = %q", stats.StopReason)
	}
	if len(blocks) != 1 || blocks[0].Text != "partial answ" {
		t.Fatalf("partial output must be kept: %+v", blocks)
	}
	var last events.Event
	asm2 := newAssembler(func(e events.Event) { last = e })
	asm2.fail("api_error", "context canceled")
	asm2.interrupt()
	asm2.emitStop()
	if last.Kind != events.KindMessageStop {
		t.Fatalf("client must see a normal end of turn, got %v", last.Kind)
	}
}
