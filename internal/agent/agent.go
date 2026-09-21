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
}

// New builds an Agent.
func New(st *store.Store, reg *llm.Registry, tr *tools.Registry, sk *skills.Service, acfg config.AgentCfg, lcfg config.LLM, log *slog.Logger) *Agent {
	return &Agent{store: st, registry: reg, tools: tr, skills: sk, acfg: acfg, lcfg: lcfg, log: log}
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

	Context     []prompt.ContextItem // AG-UI `context`: situational notes folded into the prompt
	State       json.RawMessage      // AG-UI `state`: client state folded into the prompt, read-only
	ClientTools []ClientTool         // AG-UI `tools`: client-executed tools bound for this turn only
}

// RunOutput is the persisted assistant message plus stats.
type RunOutput struct {
	UserMessage      domain.Message
	AssistantMessage domain.Message
	Stats            RunStats
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
}

// Run executes the turn. If sink is non-nil, events are emitted as they occur
// (streaming); pass nil for a buffered/non-streaming turn. The assistant
// message is persisted before Run returns, even on partial failure.
func (a *Agent) Run(ctx context.Context, in RunInput, sink events.Sink) (RunOutput, error) {
	start := time.Now()
	sess := in.Session

	// persistCtx is detached from request cancellation so that a client
	// disconnecting mid-stream never loses the turn that was already produced.
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancelPersist()

	// 1. Persist the incoming user message.
	userMsg := domain.Message{
		SessionID: sess.ID,
		Role:      domain.RoleUser,
		Blocks:    []domain.ContentBlock{{Type: domain.BlockText, Text: in.UserText}},
	}
	if err := a.store.Messages.Append(persistCtx, &userMsg); err != nil {
		return RunOutput{}, fmt.Errorf("persist user message: %w", err)
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
	einoHistory := trimHistory(historyToMessages(history), a.acfg.HistoryTokenBudget)

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
	cm, err := provider.Model(ctx, in.Model, llm.Options{MaxTokens: maxTokens, Temperature: in.Temperature})
	if err != nil {
		return fail(fmt.Errorf("build chat model: %w", err))
	}

	// 7. Build and run the ReAct agent.
	reactAgent, err := buildReactAgent(ctx, toolSess.WrapModel(cm), boundTools, systemPrompt, a.acfg.MaxSteps, clientToolReturnDirectly)
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
	if runErr != nil {
		asm.fail("api_error", runErr.Error())
	} else {
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
