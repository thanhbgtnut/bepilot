package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/tools"
)

// loopingModel never stops using tools while it has them — the failure that
// used to end a turn with "exceeds max steps". Without tools it answers.
type loopingModel struct {
	mu    *sync.Mutex
	calls *[]callRecord
	bound bool
}

type callRecord struct {
	hadTools bool
	notice   bool // the transcript ended with the wrap-up notice
}

func (m *loopingModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.bound = len(infos) > 0
	return &cp, nil
}

func (m *loopingModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	last := in[len(in)-1]
	m.mu.Lock()
	*m.calls = append(*m.calls, callRecord{hadTools: m.bound, notice: strings.Contains(last.Content, "no tools can be called")})
	n := len(*m.calls)
	m.mu.Unlock()

	if !m.bound {
		return &schema.Message{Role: schema.Assistant, Content: "final answer"}, nil
	}
	idx := 0
	args, _ := json.Marshal(map[string]any{"timezone": "UTC"})
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		Index: &idx, ID: "call_" + string(rune('a'+n)), Type: "function",
		Function: schema.FunctionCall{Name: "current_time", Arguments: string(args)},
	}}}, nil
}

func (m *loopingModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, o...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func TestTurnEndsWithAnAnswerWhenTheBudgetRunsOut(t *testing.T) {
	for _, budget := range []int{2, 4, 16} {
		reg, err := tools.NewRegistry(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		sess := reg.NewSession()
		ctx := tools.WithSession(context.Background(), sess)

		var (
			mu    sync.Mutex
			calls []callRecord
		)
		cm := sess.WrapModel(&loopingModel{mu: &mu, calls: &calls})
		ra, err := buildReactAgent(ctx, cm, sess.Executable(), "sys", budget, nil)
		if err != nil {
			t.Fatal(err)
		}

		var evs []events.Event
		asm := newAssembler(func(e events.Event) { evs = append(evs, e) })
		history := []*einoMessage{{Role: schema.User, Content: "keep going"}}
		if err := (&Agent{}).drive(ctx, ra, history, newCallbackHandler(asm)); err != nil {
			t.Fatalf("budget %d: turn failed instead of answering: %v", budget, err)
		}
		blocks, _ := asm.result("end_turn")
		var answer string
		for _, b := range blocks {
			if b.Type == "text" {
				answer += b.Text
			}
		}
		if answer != "final answer" {
			t.Errorf("budget %d: answer = %q", budget, answer)
		}
		if len(calls) != budget {
			t.Fatalf("budget %d: model was called %d times", budget, len(calls))
		}
		for i, c := range calls {
			last := i == budget-1
			if c.hadTools == last || c.notice != last {
				t.Errorf("budget %d, call %d: %+v — only the last call may run without tools and see the notice", budget, i+1, c)
			}
		}
	}
}

// recordingModel logs how many tools it was bound with and whether each call's
// transcript ended with the budget warning, and keeps calling a tool until it is
// told there is no more budget.
func TestBudgetWarnsBeforeTheLastCall(t *testing.T) {
	reg, err := tools.NewRegistry(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := reg.NewSession()
	ctx := tools.WithSession(context.Background(), sess)
	var (
		mu    sync.Mutex
		calls []callRecord
		warns []bool
	)
	cm := sess.WrapModel(&warnProbe{loopingModel: loopingModel{mu: &mu, calls: &calls}, warns: &warns})
	ra, err := buildReactAgent(ctx, cm, sess.Executable(), "sys", 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	asm := newAssembler(nil)
	if err := (&Agent{}).drive(ctx, ra, []*einoMessage{{Role: schema.User, Content: "go"}}, newCallbackHandler(asm)); err != nil {
		t.Fatal(err)
	}
	want := []bool{false, false, false, false, false, true, true, false} // calls 6 and 7: 3 and 2 left; call 8 is the wrap-up
	if len(warns) != len(want) {
		t.Fatalf("calls = %d, want %d", len(warns), len(want))
	}
	for i := range want {
		if warns[i] != want[i] {
			t.Errorf("call %d warned = %v, want %v (all: %v)", i+1, warns[i], want[i], warns)
		}
	}
}

type warnProbe struct {
	loopingModel
	warns *[]bool
}

func (m *warnProbe) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, _ := m.loopingModel.WithTools(infos)
	cp := *m
	cp.loopingModel = *(inner.(*loopingModel))
	return &cp, nil
}

func (m *warnProbe) Generate(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	*m.warns = append(*m.warns, strings.Contains(in[len(in)-1].Content, "model calls remain"))
	m.mu.Unlock()
	return m.loopingModel.Generate(ctx, in, o...)
}

func (m *warnProbe) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, o...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
