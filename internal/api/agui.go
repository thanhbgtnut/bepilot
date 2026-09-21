package api

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/agent"
	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/agent/prompt"
	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/server/middleware"
	"github.com/thanhenti/bepilot/internal/server/sse"
	"github.com/thanhenti/bepilot/internal/store"
)

// AGUIRunAgent handles POST /v1/ag-ui/run.
//
// @Summary     Run an agent turn (AG-UI protocol)
// @Description Accepts an AG-UI `RunAgentInput` and streams the turn back as AG-UI protocol Server-Sent Events: `RUN_STARTED`, `TEXT_MESSAGE_START` / `TEXT_MESSAGE_CONTENT` / `TEXT_MESSAGE_END`, `TOOL_CALL_START` / `TOOL_CALL_ARGS` / `TOOL_CALL_END` / `TOOL_CALL_RESULT`, then `RUN_FINISHED` (or `RUN_ERROR`). Each frame is a `data:` line whose JSON carries a `type` field.
// @Description `threadId` maps to a bepilot session: pass an existing session id to continue a conversation, or leave it empty to start a new one (the new id is returned in `RUN_STARTED.threadId` — persist it for the next turn). Only the last user message drives the turn; history is loaded server-side. `state` and `context` are folded into the system prompt for this turn. `tools` are bound as client-executed tools: the agent may call one, but bepilot only emits the `TOOL_CALL_*` frames and ends the run — the client must execute the tool and send the result back as a `tool` message on the next run. `forwardedProps` is accepted but has no defined effect. A request whose `messages` carries no user turn is treated as a history replay for an existing `threadId` (CopilotKit issues this when a thread becomes the active one in the UI): the response is `RUN_STARTED` + `MESSAGES_SNAPSHOT` (the session's persisted messages) + `RUN_FINISHED`, with no new turn run or saved. Such a request naming an empty or unknown `threadId` is a 400, since there is nothing to replay.
// @Tags        AG-UI
// @Accept      json
// @Produce     text/event-stream
// @Param       request  body      dto.AGUIRunAgentInput  true  "AG-UI RunAgentInput"
// @Success     200      {string}  string                 "AG-UI protocol SSE event stream"
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Failure     403      {object}  dto.ErrorResponse
// @Security    ApiKeyAuth
// @Router      /v1/ag-ui/run [post]
func (h *Handlers) AGUIRunAgent(ctx context.Context, c *app.RequestContext) {
	user, ok := middleware.UserFrom(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "unauthenticated"))
		return
	}

	var req dto.AGUIRunAgentInput
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}

	userText := strings.TrimSpace(req.LastUserText())
	if userText == "" {
		h.aguiReplay(ctx, c, user, req)
		return
	}

	provider := h.LLM.DefaultProvider
	model := h.LLM.DefaultModel

	sess, err := h.resolveAGUIThread(ctx, user, req.ThreadID, provider, model, userText)
	if err != nil {
		switch err {
		case errSessionForbidden:
			c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "thread belongs to another user"))
		default:
			h.serverError(c, err)
		}
		return
	}

	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = "run_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	threadID := sess.ID.String()

	in := agent.RunInput{
		User:        user,
		Session:     sess,
		UserText:    userText,
		Provider:    provider,
		Model:       model,
		Context:     toPromptContext(req.Context),
		State:       req.State,
		ClientTools: toClientTools(req.Tools),
	}

	w, mapper := sse.NewAGUIWriter(c, threadID, runID)
	defer w.Close()

	_, runErr := h.Agent.Run(ctx, in, func(ev events.Event) { w.Emit(ev) })

	// Guarantee a well-formed AG-UI stream even if the agent failed before
	// emitting any event (e.g. the chat model could not be built).
	if !mapper.Started() {
		w.Publish(events.AGUIFrame(map[string]any{
			"type": "RUN_STARTED", "threadId": threadID, "runId": runID,
		}))
	}
	if !mapper.Ended() {
		if runErr != nil {
			w.Publish(events.AGUIFrame(map[string]any{
				"type": "RUN_ERROR", "message": runErr.Error(), "code": "api_error",
			}))
		} else {
			w.Publish(events.AGUIFrame(map[string]any{
				"type": "RUN_FINISHED", "threadId": threadID, "runId": runID,
			}))
		}
	}
	if runErr != nil {
		h.Log.Warn("ag-ui run ended with error", "session", sess.ID, "err", runErr)
	}
}

// aguiReplay serves an AG-UI run request that carries no user turn — no
// `messages` at all, or none with role "user". CopilotKit's core issues
// exactly this the first time a thread becomes "explicit" in the UI (right
// after RUN_FINISHED promotes a freshly-minted thread id, or when the user
// picks an existing conversation): it clears its local message list and
// expects the transport to hand back the thread's history so it can
// repopulate. bepilot's AG-UI transport has no separate connect/replay verb,
// so this reuses the run endpoint: rather than 400 on the missing user
// message, it replies with the persisted history as a single
// MESSAGES_SNAPSHOT. A threadId that doesn't resolve to the caller's session
// has nothing to replay and is still a 400 — this path only ever returns
// history, never starts one.
func (h *Handlers) aguiReplay(ctx context.Context, c *app.RequestContext, user domain.User, req dto.AGUIRunAgentInput) {
	const noHistoryMsg = "messages must contain at least one user message with text content"

	id, err := uuid.Parse(strings.TrimSpace(req.ThreadID))
	if err != nil {
		h.badRequest(c, noHistoryMsg)
		return
	}
	sess, err := h.Store.Sessions.Get(ctx, id)
	switch {
	case err == store.ErrNotFound:
		h.badRequest(c, noHistoryMsg)
		return
	case err != nil:
		h.serverError(c, err)
		return
	}
	if sess.UserID != user.ID {
		c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "thread belongs to another user"))
		return
	}

	history, err := h.Store.Messages.ListBySession(ctx, sess.ID, 0, 500)
	if err != nil {
		h.serverError(c, err)
		return
	}

	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = "run_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	threadID := sess.ID.String()

	w, _ := sse.NewAGUIWriter(c, threadID, runID)
	defer w.Close()
	w.Publish(events.AGUIFrame(map[string]any{
		"type": "RUN_STARTED", "threadId": threadID, "runId": runID,
	}))
	w.Publish(events.AGUIFrame(map[string]any{
		"type": "MESSAGES_SNAPSHOT", "messages": aguiMessageSnapshot(history),
	}))
	w.Publish(events.AGUIFrame(map[string]any{
		"type": "RUN_FINISHED", "threadId": threadID, "runId": runID,
	}))
}

// aguiMessageSnapshot flattens persisted messages into the plain {id, role,
// content} shape AG-UI's MESSAGES_SNAPSHOT expects. Only text is carried —
// like the live TEXT_MESSAGE_* stream, thinking blocks are dropped, and tool
// calls/results are omitted since replay only needs to restore the visible
// transcript, not let the model resume mid-tool-call.
func aguiMessageSnapshot(msgs []domain.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		var role string
		switch m.Role {
		case domain.RoleUser:
			role = "user"
		case domain.RoleAssistant:
			role = "assistant"
		default:
			continue
		}
		var text strings.Builder
		for _, b := range m.Blocks {
			if b.Type == domain.BlockText {
				text.WriteString(b.Text)
			}
		}
		content := text.String()
		if content == "" {
			continue
		}
		out = append(out, map[string]any{"id": m.ID.String(), "role": role, "content": content})
	}
	return out
}

// resolveAGUIThread maps an AG-UI threadId to a bepilot session. A threadId
// that is a valid id of a session the caller owns continues that conversation.
// A well-formed but unknown UUID threadId starts a fresh session pinned to
// that exact id — AG-UI clients (CopilotKit's HttpAgent included) mint their
// own threadId once and resend it unchanged on every turn, so the session
// must be created under that id or every subsequent turn would again find
// nothing and spawn yet another disconnected session, and the conversation
// would never accumulate history. Anything else (empty or not a UUID at all)
// starts a fresh session with a server-generated id. Only a session owned by
// someone else is an error.
func (h *Handlers) resolveAGUIThread(ctx context.Context, user domain.User, threadID, provider, model, firstText string) (domain.Session, error) {
	id, parseErr := uuid.Parse(strings.TrimSpace(threadID))
	if parseErr == nil {
		sess, err := h.Store.Sessions.Get(ctx, id)
		switch {
		case err == nil:
			if sess.UserID != user.ID {
				return domain.Session{}, errSessionForbidden
			}
			return sess, nil
		case err == store.ErrNotFound:
			// fall through to create, pinned to this id
		default:
			return domain.Session{}, err
		}
	}

	title := firstText
	if len(title) > 60 {
		title = strings.TrimSpace(title[:60]) + "…"
	}
	var meta map[string]any
	if s := strings.TrimSpace(threadID); s != "" && parseErr != nil {
		// Not a UUID, so it can't become the session's primary key — keep it
		// around for reference instead of silently dropping it.
		meta = map[string]any{"agui_thread_id": s}
	}
	return h.Store.Sessions.Create(ctx, store.CreateParams{
		ID:       id, // uuid.Nil (parseErr != nil) lets Postgres generate one
		UserID:   user.ID,
		Title:    title,
		Provider: provider,
		Model:    model,
		Metadata: meta,
	})
}

// toPromptContext maps AG-UI `context` items onto the agent's prompt.ContextItem.
func toPromptContext(items []dto.AGUIContextItem) []prompt.ContextItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]prompt.ContextItem, 0, len(items))
	for _, it := range items {
		out = append(out, prompt.ContextItem{Description: it.Description, Value: it.Value})
	}
	return out
}

// toClientTools maps AG-UI `tools` definitions onto the agent's ClientTool.
func toClientTools(defs []dto.AGUIToolDefinition) []agent.ClientTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]agent.ClientTool, 0, len(defs))
	for _, d := range defs {
		out = append(out, agent.ClientTool{Name: d.Name, Description: d.Description, Parameters: d.Parameters})
	}
	return out
}
