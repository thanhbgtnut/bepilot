// Package agent runs one assistant turn: it loads conversation history, builds
// the dynamic system prompt (environment + retrieved skills + style), runs an
// Eino ReAct agent with streaming, maps the run to an Anthropic-style event
// stream, and persists the result.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/agent/prompt"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/llm"
	"github.com/thanhenti/bepilot/internal/skills"
	"github.com/thanhenti/bepilot/internal/store"
	"github.com/thanhenti/bepilot/internal/tools"
)

// Agent is the long-lived turn executor.
type Agent struct {
	store    *store.Store
	registry *llm.Registry
	tools    *tools.Registry
	skills   *skills.Service
	acfg     config.AgentCfg
	lcfg     config.LLM
	log      *slog.Logger
	runs     *runRegistry // the turn running on each session, if any
}

// New builds an Agent.
func New(st *store.Store, reg *llm.Registry, tr *tools.Registry, sk *skills.Service, acfg config.AgentCfg, lcfg config.LLM, log *slog.Logger) *Agent {
	return &Agent{store: st, registry: reg, tools: tr, skills: sk, acfg: acfg, lcfg: lcfg, log: log, runs: newRunRegistry()}
}

// RunInput describes one turn request.
type RunInput struct {
	User          domain.User
	Session       domain.Session
	UserText      string
	Provider      string
	Model         string
	MaxTokens     int
	Temperature   *float32
	RequestSystem string // request-level `system` field, appended to the prompt

	// HistoryTokenBudget overrides the configured history budget for this turn
	// when > 0.
	HistoryTokenBudget int

	Context     []prompt.ContextItem // AG-UI `context`: situational notes folded into the prompt
	State       json.RawMessage      // AG-UI `state`: client state folded into the prompt, read-only
	ClientTools []ClientTool         // AG-UI `tools`: client-executed tools bound for this turn only

	// preSaved is a user message that is already stored (one accepted while a
	// turn was running); the turn answers it instead of storing it again.
	preSaved *domain.Message
}

// RunOutput is the persisted assistant message plus stats.
type RunOutput struct {
	UserMessage      domain.Message
	AssistantMessage domain.Message
	Stats            RunStats

	// Steered is true when the session already had a turn running and this
	// message was handed to it instead of starting another: the reply arrives
	// on the running turn's stream, and AssistantMessage is empty.
	Steered bool
}

// RunStats is exported observability for one turn.
type RunStats struct {
	Provider     string
	Model        string
	StopReason   string
	InputTokens  int
	OutputTokens int
	Steps        int
	LatencyMS    int
	SkillsFound  []string
	Detail       map[string]any // stored with the run; see domain.AgentRun.Detail
}

// Run executes the turn. If sink is non-nil, events are emitted as they occur
// (streaming); pass nil for a buffered/non-streaming turn. The assistant
// message is persisted before Run returns, even on partial failure.
//
// A session runs one turn at a time, but a message never has to wait for it:
//   - a message sent while a turn is running is handed to that turn, which reads
//     it before its next step (RunOutput.Steered) — the reply arrives on the
//     running turn's stream;
//   - a short "stop" message interrupts the running turn, which keeps what it
//     produced so far and ends with stop reason "interrupted"; the message then
//     runs as an ordinary turn.
func (a *Agent) Run(ctx context.Context, in RunInput, sink events.Sink) (RunOutput, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sid := in.Session.ID

	for {
		ar, owner := a.runs.acquire(sid, cancel)
		if owner {
			out, err := a.runTurn(runCtx, ar, in, sink)
			left := append(ar.left, ar.steer.close()...)
			a.runs.release(sid, ar)
			a.resume(in, left)
			return out, err
		}

		if in.preSaved == nil && isStopRequest(in.UserText) {
			ar.interrupt()
			if err := waitRun(ctx, ar); err != nil {
				return RunOutput{}, err
			}
			continue
		}

		if in.preSaved == nil {
			msg, err := a.persistUser(ctx, sid, in.UserText)
			if err != nil {
				return RunOutput{}, err
			}
			in.preSaved = &msg
		}
		if ar.steer.push(steerMsg{Text: in.UserText, Msg: *in.preSaved}) {
			return RunOutput{UserMessage: *in.preSaved, Steered: true}, nil
		}
		// The turn stopped reading its inbox a moment ago; let it finish, then
		// run this message as a turn of its own.
		if err := waitRun(ctx, ar); err != nil {
			return RunOutput{}, err
		}
	}
}

// waitRun blocks until the given turn has been persisted and released.
func waitRun(ctx context.Context, ar *activeRun) error {
	select {
	case <-ar.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// persistUser stores an incoming user message. It is detached from request
// cancellation so a client disconnecting never loses a message it sent.
func (a *Agent) persistUser(ctx context.Context, sessionID uuid.UUID, text string) (domain.Message, error) {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	msg := domain.Message{
		SessionID: sessionID,
		Role:      domain.RoleUser,
		Blocks:    []domain.ContentBlock{{Type: domain.BlockText, Text: text}},
	}
	if err := a.store.Messages.Append(pctx, &msg); err != nil {
		return domain.Message{}, fmt.Errorf("persist user message: %w", err)
	}
	return msg, nil
}

// resume answers messages that were accepted by a turn that ended before its
// model could read them. They are already stored; without this they would sit
// in the history unanswered until the user wrote again. The reply is stored,
// not streamed (the request that sent them has long returned).
func (a *Agent) resume(in RunInput, left []steerMsg) {
	if len(left) == 0 {
		return
	}
	last := left[len(left)-1]
	next := in
	next.UserText = last.Text
	next.preSaved = &last.Msg
	next.ClientTools = nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := a.Run(ctx, next, nil); err != nil {
			a.log.Warn("follow-up turn for late messages failed", "session", in.Session.ID, "err", err)
		}
	}()
}

// runTurn is one turn, run as the session's only active turn.
func (a *Agent) runTurn(ctx context.Context, ar *activeRun, in RunInput, sink events.Sink) (RunOutput, error) {
	start := time.Now()
	sess := in.Session

	// persistCtx is detached from request cancellation so that a client
	// disconnecting mid-stream never loses the turn that was already produced.
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancelPersist()

	// 1. Persist the incoming user message (unless it was stored already).
	var userMsg domain.Message
	if in.preSaved != nil {
		userMsg = *in.preSaved
	} else {
		var err error
		if userMsg, err = a.persistUser(ctx, sess.ID, in.UserText); err != nil {
			return RunOutput{}, err
		}
	}

	// asm accumulates events/content for the whole turn. It is created here
	// (rather than just before driving the model) so that a setup failure
	// below — bad client tool schema, unknown provider, model construction
	// error — still produces a persisted assistant message via fail(), the
	// same way a failure during generation already does. Without this, a
	// setup-phase error left the user's message saved with no assistant
	// counterpart and no record the turn ever ran.
	asm := newAssembler(sink)
	fail := func(err error) (RunOutput, error) {
		asm.fail("api_error", err.Error())
		blocks, stats := asm.result("error")
		if len(blocks) == 0 {
			blocks = []domain.ContentBlock{{Type: domain.BlockText, Text: ""}}
		}
		assistantMsg := domain.Message{
			SessionID:  sess.ID,
			Role:       domain.RoleAssistant,
			StopReason: stats.StopReason,
			Usage:      domain.Usage{InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens},
			Blocks:     blocks,
		}
		if perr := a.store.Messages.Append(persistCtx, &assistantMsg); perr != nil {
			a.log.Error("persist assistant message failed", "session", sess.ID, "err", perr)
		}
		// Only now — after the message is durably stored — tell the client the
		// turn is over. See assembler.emitStop's doc for why the order matters.
		asm.emitStop()
		a.recordRun(persistCtx, sess.ID, assistantMsg.ID, RunStats{Provider: in.Provider, Model: in.Model, StopReason: stats.StopReason}, err)
		_ = a.store.Sessions.Touch(persistCtx, sess.ID)
		return RunOutput{UserMessage: userMsg, AssistantMessage: assistantMsg}, err
	}

	// 2. Load and shape history.
	history, err := a.store.Messages.ListBySession(ctx, sess.ID, 0, 500)
	if err != nil {
		return fail(fmt.Errorf("load history: %w", err))
	}
	historyBudget := a.acfg.HistoryTokenBudget
	if in.HistoryTokenBudget > 0 {
		historyBudget = in.HistoryTokenBudget
	}
	einoHistory := trimHistory(historyToMessages(history), historyBudget)

	// 3. Retrieve skills relevant to the conversation (not just this message).
	retrievalQuery := strings.TrimSpace(in.UserText + "\n" + sess.Summary)
	matches := a.skills.Retrieve(ctx, retrievalQuery, a.acfg.SkillTopK)
	skillRefs := make([]prompt.SkillRef, 0, len(matches))
	skillSlugs := make([]string, 0, len(matches))
	toolSess := a.tools.NewSession()
	// Tools an earlier turn loaded via tool_search stay loaded; a retrieved
	// skill's allowed_tools are loaded up front, no search needed.
	toolSess.Restore(einoHistory)
	for _, m := range matches {
		skillRefs = append(skillRefs, prompt.SkillRef{Slug: m.Slug, Name: m.Name, Description: m.Description, Score: m.Score})
		skillSlugs = append(skillSlugs, m.Slug)
		toolSess.Discover(m.AllowedTools...)
	}

	// 4. Resolve tools: built-ins are always bound; external (deferred) tools
	// are only callable once loaded through tool_search. This turn's AG-UI
	// client tools are bound directly. Describe what is bound for the prompt.
	clientTools, clientToolReturnDirectly, err := buildClientTools(in.ClientTools)
	if err != nil {
		return fail(err)
	}
	toolDesc := toolSess.VisibleDescriptions()
	for _, t := range clientTools {
		if info, err := t.Info(ctx); err == nil {
			toolSess.Pin(info.Name)
			toolDesc[info.Name] = info.Desc
		}
	}
	boundTools := append(toolSess.Executable(), clientTools...)
	ctx = tools.WithSession(ctx, toolSess)

	// 5. Build the dynamic system prompt.
	sysOverride := strings.TrimSpace(strings.Join([]string{sess.SystemOverride, in.RequestSystem}, "\n\n"))
	tc := prompt.TurnContext{
		MaxSteps:       a.acfg.MaxSteps,
		Now:            time.Now(),
		Provider:       in.Provider,
		Model:          in.Model,
		SessionID:      sess.ID.String(),
		UserName:       in.User.Name,
		Identity:       a.acfg.Identity,
		ResponseStyle:  a.acfg.ResponseStyle,
		SystemOverride: sysOverride,
		Summary:        sess.Summary,
		Tools:          toolDesc,
		DeferredTools:  toolSess.DeferredNames(),
		Skills:         skillRefs,
		Context:        in.Context,
		State:          strings.TrimSpace(string(in.State)),
	}
	systemPrompt := prompt.Build(tc)

	// 6. Resolve the chat model.
	provider, err := a.registry.Get(in.Provider)
	if err != nil {
		return fail(err)
	}
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = a.lcfg.MaxTokens
	}
	temperature := in.Temperature
	if temperature == nil {
		temperature = a.lcfg.Temperature
	}
	cm, err := provider.Model(ctx, in.Model, llm.Options{MaxTokens: maxTokens, Temperature: temperature})
	if err != nil {
		return fail(fmt.Errorf("build chat model: %w", err))
	}

	// 7. Build and run the ReAct agent.
	reactAgent, err := buildReactAgent(ctx, toolSess.WrapModel(cm), boundTools, systemPrompt, a.acfg.MaxSteps, clientToolReturnDirectly, ar.steer)
	if err != nil {
		return fail(fmt.Errorf("build react agent: %w", err))
	}

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	estIn := approxTokens(systemPrompt)
	for _, m := range einoHistory {
		estIn += approxTokens(m.Content)
	}
	asm.messageStart(messageID, in.Model, sess.ID.String(), estIn)

	handler := newCallbackHandler(asm)
	runErr := a.drive(ctx, reactAgent, einoHistory, handler)
	// The model has read its inbox for the last time. Anything that arrives from
	// here on is run as a turn of its own; what arrived too late for the model
	// is answered by a follow-up turn (see resume).
	ar.left = append(ar.left, ar.steer.close()...)
	switch {
	case runErr != nil && ar.interrupted.Load():
		// The user asked for this: keep what was produced, end without an error.
		asm.interrupt()
		runErr = nil
	case runErr != nil:
		asm.fail("api_error", runErr.Error())
	default:
		asm.finish("end_turn")
	}

	// 8. Persist the assistant message (including any partial content).
	//
	// This gets its own freshly-timed context rather than reusing persistCtx
	// from the top of Run: persistCtx's 20s budget starts ticking before
	// generation even begins, so on a turn slow enough to run past it (a
	// tool round-trip, a loaded model, contention under concurrent traffic —
	// none of them rare), it had already expired by the time execution
	// reached here. The turn still streamed to the client and looked
	// successful, but the write below failed immediately with "context
	// deadline exceeded" and only a log line recorded it: the reply was
	// visible during the chat and gone on the next reload, with nothing
	// surfaced to the caller to explain why.
	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancelFinish()

	blocks, stats := asm.result("end_turn")
	if n := asm.fixedJSONValues(); n > 0 {
		a.log.Warn("evaluated arithmetic the model left inside JSON values", "session", sess.ID, "values", n, "model", in.Model)
	}
	assistantMsg := domain.Message{
		SessionID:  sess.ID,
		Role:       domain.RoleAssistant,
		StopReason: stats.StopReason,
		Usage:      domain.Usage{InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens},
		Blocks:     blocks,
	}
	if len(blocks) == 0 && runErr != nil {
		assistantMsg.Blocks = []domain.ContentBlock{{Type: domain.BlockText, Text: ""}}
	}
	if err := a.store.Messages.Append(finishCtx, &assistantMsg); err != nil {
		a.log.Error("persist assistant message failed", "session", sess.ID, "err", err)
	}
	// Only now — after the message is durably stored — tell the client the
	// turn is over. See assembler.emitStop's doc for why the order matters:
	// emitting RUN_FINISHED/RUN_ERROR earlier lets a client that reacts by
	// re-reading the session over REST race the write and see the turn as if
	// it never happened.
	asm.emitStop()

	runStats := RunStats{
		Provider: provider.Name(), Model: in.Model,
		StopReason: stats.StopReason, InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens,
		Steps: stats.Steps, LatencyMS: int(time.Since(start).Milliseconds()), SkillsFound: skillSlugs,
		Detail: runDetail(stats.StopReason, maxTokens, temperature, blocks, skillSlugs, len(einoHistory), ar),
	}
	a.recordRun(finishCtx, sess.ID, assistantMsg.ID, runStats, runErr)
	_ = a.store.Sessions.Touch(finishCtx, sess.ID)
	a.maybeSummarize(sess, len(history)+1, provider, in.Model)

	out := RunOutput{UserMessage: userMsg, AssistantMessage: assistantMsg, Stats: runStats}
	if runErr != nil {
		return out, runErr
	}
	return out, nil
}

// drive runs the ReAct agent in streaming mode and drains the top-level stream
// so the graph runs to completion. All content is emitted via callbacks.
func (a *Agent) drive(ctx context.Context, ra reactAgent, history []*einoMessage, handler callbackHandler) error {
	stream, err := ra.Stream(ctx, history, agent.WithComposeOptions(compose.WithCallbacks(handler)))
	if err != nil {
		return err
	}
	defer stream.Close()
	for {
		_, err := stream.Recv()
		if err != nil {
			if isEOF(err) {
				return nil
			}
			return err
		}
	}
}

func (a *Agent) recordRun(ctx context.Context, sessionID, msgID uuid.UUID, s RunStats, runErr error) {
	rec := domain.AgentRun{
		SessionID: sessionID, Provider: s.Provider, Model: s.Model,
		Steps: s.Steps, TokensIn: s.InputTokens, TokensOut: s.OutputTokens, LatencyMS: s.LatencyMS,
		Detail: s.Detail,
	}
	if msgID != uuid.Nil {
		rec.MessageID = &msgID
	}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	if err := a.store.Runs.Insert(ctx, rec); err != nil {
		a.log.Warn("record agent run failed", "err", err)
	}
}

// runDetail describes how a turn ran, for agent_runs.detail. It is what makes
// two runs of the same request comparable: same settings? same tools? did one
// stop because of the output limit?
func runDetail(stopReason string, maxTokens int, temperature *float32, blocks []domain.ContentBlock, skills []string, historyMessages int, ar *activeRun) map[string]any {
	d := map[string]any{
		"stop_reason":      stopReason,
		"max_tokens":       maxTokens,
		"history_messages": historyMessages,
	}
	if temperature != nil {
		d["temperature"] = *temperature
	}
	tools := map[string]int{}
	for _, b := range blocks {
		if b.Type == domain.BlockToolUse && b.ToolName != "" {
			tools[b.ToolName]++
		}
	}
	if len(tools) > 0 {
		d["tool_calls"] = tools
	}
	if len(skills) > 0 {
		d["skills"] = skills
	}
	if n := ar.steer.accepted(); n > 0 {
		d["steered_messages"] = n
	}
	if ar.interrupted.Load() {
		d["interrupted"] = true
	}
	return d
}
