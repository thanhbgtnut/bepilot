package api

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
	hsse "github.com/hertz-contrib/sse"

	"github.com/thanhenti/bepilot/internal/agent"
	"github.com/thanhenti/bepilot/internal/agent/events"
	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/server/middleware"
	"github.com/thanhenti/bepilot/internal/server/sse"
	"github.com/thanhenti/bepilot/internal/store"
)

// Messages handles POST /v1/messages.
//
// @Summary     Create a message (streaming or buffered)
// @Description Runs one agent turn. With `stream: true` (or `Accept: text/event-stream`) the response is Server-Sent Events using Anthropic frame types (`message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`, `ping`, `error`) plus the extension events `tool_execution_start` / `tool_execution_stop`. `message_start.message.session_id` is a bepilot extension carrying the session the turn was persisted under — capture it (when `metadata.session_id` was omitted, a new session is created) and pass it back as `metadata.session_id` on the next call to continue the conversation. Otherwise a single JSON message is returned, whose `session` field carries the same information.
// @Description A session runs one turn at a time, but a message never has to wait for it. If a turn is already running on the session, the new message is handed to that turn, which reads it before its next step: the response is `202` with `{"type":"steered"}` (or, when streaming, a single `steered` event) and the reply arrives on the stream of the turn that is running. A short stop message ("stop", "dừng", "hủy") instead interrupts the running turn, which keeps what it produced and ends with `stop_reason: "interrupted"`, and the message then runs as a normal turn.
// @Tags        Messages
// @Accept      json
// @Produce     json
// @Produce     text/event-stream
// @Param       request  body      dto.MessagesRequest  true  "Message request"
// @Success     200      {object}  dto.MessageResponse
// @Success     202      {object}  dto.SteeredResponse
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Failure     403      {object}  dto.ErrorResponse
// @Failure     404      {object}  dto.ErrorResponse
// @Security    ApiKeyAuth
// @Router      /v1/messages [post]
func (h *Handlers) Messages(ctx context.Context, c *app.RequestContext) {
	user, ok := middleware.UserFrom(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "unauthenticated"))
		return
	}

	var req dto.MessagesRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}

	userText := strings.TrimSpace(req.LastUserText())
	if userText == "" {
		h.badRequest(c, "messages must contain at least one user message with text content")
		return
	}

	if req.HistoryTokenBudget < 0 {
		h.badRequest(c, "history_token_budget must be >= 0")
		return
	}

	provider := req.Provider
	if provider == "" {
		provider = h.LLM.DefaultProvider
	}
	if _, err := h.Registry.Get(provider); err != nil {
		h.badRequest(c, err.Error())
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = h.LLM.DefaultModel
	}

	// Resolve or create the session.
	sess, err := h.resolveSession(ctx, user, req, provider, model, userText)
	if err != nil {
		switch err {
		case errSessionNotFound:
			h.notFound(c, "session not found")
		case errSessionForbidden:
			c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "session belongs to another user"))
		default:
			h.serverError(c, err)
		}
		return
	}

	if req.Metadata != nil && req.Metadata.UserID != "" && req.Metadata.UserID != user.ID.String() {
		c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "metadata.user_id does not match the authenticated user"))
		return
	}

	in := agent.RunInput{
		User:          user,
		Session:       sess,
		UserText:      userText,
		Provider:      provider,
		Model:         model,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		RequestSystem: dto.SystemText(req.System),

		HistoryTokenBudget: req.HistoryTokenBudget,
	}

	if req.Stream || strings.Contains(string(c.GetHeader("Accept")), "text/event-stream") {
		h.streamTurn(ctx, c, in)
		return
	}
	h.bufferedTurn(ctx, c, in)
}

func (h *Handlers) bufferedTurn(ctx context.Context, c *app.RequestContext, in agent.RunInput) {
	out, err := h.Agent.Run(ctx, in, nil)
	if err != nil && len(out.AssistantMessage.Blocks) == 0 {
		h.serverError(c, err)
		return
	}
	brief := dto.BriefFromDomain(in.Session)
	if out.Steered {
		c.JSON(consts.StatusAccepted, dto.SteeredResponse{Type: "steered", MessageID: messageID(out.UserMessage.ID), Session: &brief})
		return
	}
	resp := dto.MessageResponse{
		ID:         messageID(out.AssistantMessage.ID),
		Type:       "message",
		Role:       "assistant",
		Model:      in.Model,
		Content:    dto.BlocksToOutput(out.AssistantMessage.Blocks),
		StopReason: out.AssistantMessage.StopReason,
		Usage: dto.Usage{
			InputTokens:  out.AssistantMessage.Usage.InputTokens,
			OutputTokens: out.AssistantMessage.Usage.OutputTokens,
		},
		Session: &brief,
	}
	c.JSON(consts.StatusOK, resp)
}

func (h *Handlers) streamTurn(ctx context.Context, c *app.RequestContext, in agent.RunInput) {
	w := sse.NewWriter(c, 0)
	defer w.Close()

	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go w.RunPinger(pingCtx, h.Agentcfg.PingInterval)

	sink := func(ev events.Event) { w.Emit(ev) }
	out, err := h.Agent.Run(ctx, in, sink)
	if err != nil {
		h.Log.Warn("stream turn ended with error", "session", in.Session.ID, "err", err)
		// The agent already emitted an `error` event via the sink.
	}
	if out.Steered {
		// Nothing was streamed: the reply comes on the running turn's stream.
		w.Publish(&hsse.Event{Event: "steered", Data: []byte(`{"type":"steered","message_id":"` + messageID(out.UserMessage.ID) + `"}`)})
	}
}

// --- session resolution -------------------------------------------------—-—-

var (
	errSessionNotFound  = &sessionErr{"not found"}
	errSessionForbidden = &sessionErr{"forbidden"}
)

type sessionErr struct{ s string }

func (e *sessionErr) Error() string { return "session " + e.s }

func (h *Handlers) resolveSession(ctx context.Context, user domain.User, req dto.MessagesRequest, provider, model, firstText string) (domain.Session, error) {
	if req.Metadata != nil && req.Metadata.SessionID != "" {
		id, err := uuid.Parse(req.Metadata.SessionID)
		if err != nil {
			return domain.Session{}, errSessionNotFound
		}
		sess, err := h.Store.Sessions.Get(ctx, id)
		if err == store.ErrNotFound {
			return domain.Session{}, errSessionNotFound
		}
		if err != nil {
			return domain.Session{}, err
		}
		if sess.UserID != user.ID {
			return domain.Session{}, errSessionForbidden
		}
		return sess, nil
	}

	title := sessionTitle(firstText, 60)
	return h.Store.Sessions.Create(ctx, store.CreateParams{
		UserID:   user.ID,
		Title:    title,
		Provider: provider,
		Model:    model,
	})
}

func messageID(id uuid.UUID) string {
	if id == uuid.Nil {
		return "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return "msg_" + strings.ReplaceAll(id.String(), "-", "")
}
