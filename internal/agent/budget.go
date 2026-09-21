package agent

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// wrapUpNotice is added to the transcript for the last model call a turn is
// allowed, which runs with no tools bound.
const wrapUpNotice = "[Notice from the system] You have used all the tool calls available for this turn, so no tools can be called now. Write your final answer to the user now, in the user's language, using what you have gathered so far. If part of the request could not be completed, say exactly which part and why."

// budgetWarnCalls is how many calls before the last one the model is warned.
const budgetWarnCalls = 3

// budgetWarnNotice is added to the transcript of each of the last few calls that
// still have tools. It must not contain the wrap-up notice's wording.
const budgetWarnNotice = "[Notice from the system] Only %d model calls remain in this turn, this one included, and the final call has no tools. Stop exploring: do what is still essential (batch independent tool calls into one step) and get ready to give a complete final answer. Put the most important part first; if something will not fit, say what is left."

// graphStepsFor is how many graph steps the ReAct loop needs for n model
// calls. eino counts every node execution, and a model call followed by its
// tool round is two of them; the extra covers the entry and exit nodes. The
// call budget below always ends the loop first, so this is only a backstop.
func graphStepsFor(n int) int { return 2*n + 10 }

// budgetModel caps how many times one turn may call the model. Hitting eino's
// own step limit aborts the turn with "exceeds max steps" and throws away
// everything the model had worked out; a model that loops on tools, or a task
// that genuinely needs many rounds, then ends in an error instead of an answer.
// Here the last permitted call runs with no tools bound and a notice to answer,
// so the model can only finish — the turn always ends with a reply.
type budgetModel struct {
	base  model.ToolCallingChatModel // no tools bound: used for the wrap-up call
	bound model.ToolCallingChatModel // tools bound; nil until WithTools
	calls *atomic.Int32              // shared by every copy made by WithTools
	max   int32
	steer *steerer // messages the user sent while the turn runs; nil = none
}

func newBudgetModel(cm model.ToolCallingChatModel, maxCalls int, steer *steerer) *budgetModel {
	return &budgetModel{base: cm, calls: new(atomic.Int32), max: int32(maxCalls), steer: steer}
}

func (b *budgetModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := b.base.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &budgetModel{base: b.base, bound: bound, calls: b.calls, max: b.max, steer: b.steer}, nil
}

// pick chooses the model and transcript for the next call.
func (b *budgetModel) pick(in []*schema.Message) (model.BaseChatModel, []*schema.Message) {
	n := b.calls.Add(1)
	if b.steer != nil {
		// Every model call is a safe point to read the user's new messages: the
		// transcript here always ends on a user or tool message, never inside a
		// tool_use / tool_result pair.
		in = b.steer.apply(in)
	}
	if b.bound == nil {
		return b.base, in
	}
	if n >= b.max {
		return b.base, append(in[:len(in):len(in)], schema.UserMessage(wrapUpNotice))
	}
	if left := int(b.max - n + 1); left <= budgetWarnCalls && int(b.max) > budgetWarnCalls+1 {
		// Tell the model while it can still act on it, instead of cutting it off
		// mid-task on the last call.
		return b.bound, append(in[:len(in):len(in)], schema.UserMessage(fmt.Sprintf(budgetWarnNotice, left)))
	}
	return b.bound, in
}

func (b *budgetModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m, in := b.pick(in)
	return generateResuming(ctx, m, in, opts...)
}

func (b *budgetModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m, in := b.pick(in)
	return streamResuming(ctx, m, in, opts...)
}

// IsCallbacksEnabled mirrors the wrapped model so eino neither double-fires nor
// drops model callbacks (see tools.sessionModel).
func (b *budgetModel) IsCallbacksEnabled() bool { return components.IsCallbacksEnabled(b.base) }

var (
	_ model.ToolCallingChatModel = (*budgetModel)(nil)
	_ components.Checker         = (*budgetModel)(nil)
)
