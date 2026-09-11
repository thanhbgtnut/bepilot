package api

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

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
// @Description Runs one agent turn. With `stream: true` (or `Accept: text/event-stream`) the response is Server-Sent Events using Anthropic frame types (`message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`, `ping`, `error`) plus the extension events `tool_execution_start` / `tool_execution_stop`. Otherwise a single JSON message is returned.
// @Tags        Messages
// @Accept      json
// @Produce     json
// @Produce     text/event-stream
// @Param       request  body      dto.MessagesRequest  true  "Message request"
// @Success     200      {object}  dto.MessageResponse
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
	if _, err := h.Agent.Run(ctx, in, sink); err != nil {
		h.Log.Warn("stream turn ended with error", "session", in.Session.ID, "err", err)
		// The agent already emitted an `error` event via the sink.
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

	title := firstText
	if len(title) > 60 {
		title = strings.TrimSpace(title[:60]) + "…"
	}
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
