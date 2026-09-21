package api

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/server/middleware"
	"github.com/thanhenti/bepilot/internal/store"
)

// CreateSession handles POST /v1/sessions.
//
// @Summary   Create a session explicitly
// @Tags      Sessions
// @Accept    json
// @Produce   json
// @Param     request  body      dto.CreateSessionRequest  false  "Session fields"
// @Success   201      {object}  dto.SessionBrief
// @Failure   401      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions [post]
func (h *Handlers) CreateSession(ctx context.Context, c *app.RequestContext) {
	user, _ := middleware.UserFrom(c)
	var req dto.CreateSessionRequest
	if err := c.BindJSON(&req); err != nil && len(c.Request.Body()) > 0 {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	provider := req.Provider
	if provider == "" {
		provider = h.LLM.DefaultProvider
	}
	model := req.Model
	if model == "" {
		model = h.LLM.DefaultModel
	}
	sess, err := h.Store.Sessions.Create(ctx, store.CreateParams{
		UserID:         user.ID,
		Title:          req.Title,
		Provider:       provider,
		Model:          model,
		SystemOverride: req.System,
		Metadata:       req.Metadata,
	})
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, dto.BriefFromDomain(sess))
}

// ListSessions handles GET /v1/sessions — the "sessions by user" endpoint.
//
// @Summary   List the authenticated user's sessions
// @Tags      Sessions
// @Produce   json
// @Param     limit   query     int     false  "Page size (1-100)"  default(30)
// @Param     cursor  query     string  false  "Opaque cursor from a previous response's next_cursor"
// @Success   200     {object}  dto.SessionList
// @Failure   401     {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions [get]
func (h *Handlers) ListSessions(ctx context.Context, c *app.RequestContext) {
	user, _ := middleware.UserFrom(c)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	before := decodeCursor(c.Query("cursor"))

	sessions, err := h.Store.Sessions.ListByUser(ctx, user.ID, before, limit+1)
	if err != nil {
		h.serverError(c, err)
		return
	}

	resp := dto.SessionList{Data: []dto.SessionBrief{}}
	if len(sessions) > limit {
		resp.HasMore = true
		last := sessions[limit-1]
		resp.NextCursor = encodeCursor(last.UpdatedAt)
		sessions = sessions[:limit]
	}
	for _, s := range sessions {
		resp.Data = append(resp.Data, dto.BriefFromDomain(s))
	}
	c.JSON(consts.StatusOK, resp)
}

// GetSession handles GET /v1/sessions/{id} — session plus reconstructed transcript.
//
// @Summary   Get a session with its reconstructed transcript
// @Tags      Sessions
// @Produce   json
// @Param     id   path      string  true  "Session id"  format(uuid)
// @Success   200  {object}  dto.SessionDetail
// @Failure   401  {object}  dto.ErrorResponse
// @Failure   403  {object}  dto.ErrorResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions/{id} [get]
func (h *Handlers) GetSession(ctx context.Context, c *app.RequestContext) {
	sess, ok := h.ownedSession(ctx, c)
	if !ok {
		return
	}
	msgs, err := h.Store.Messages.ListBySession(ctx, sess.ID, 0, 1000)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.SessionDetail{
		SessionBrief: dto.BriefFromDomain(sess),
		Messages:     dto.TranscriptFromMessages(msgs),
	})
}

// ListSessionMessages handles GET /v1/sessions/{id}/messages.
//
// @Summary   List a session's messages
// @Tags      Sessions
// @Produce   json
// @Param     id         path      string  true   "Session id"  format(uuid)
// @Param     after_seq  query     int     false  "Return messages with seq greater than this"  default(0)
// @Param     limit      query     int     false  "Page size"                                   default(200)
// @Success   200        {object}  dto.MessageListResponse
// @Failure   401        {object}  dto.ErrorResponse
// @Failure   403        {object}  dto.ErrorResponse
// @Failure   404        {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions/{id}/messages [get]
func (h *Handlers) ListSessionMessages(ctx context.Context, c *app.RequestContext) {
	sess, ok := h.ownedSession(ctx, c)
	if !ok {
		return
	}
	afterSeq, _ := strconv.Atoi(c.DefaultQuery("after_seq", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	msgs, err := h.Store.Messages.ListBySession(ctx, sess.ID, afterSeq, limit)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.MessageListResponse{Data: dto.TranscriptFromMessages(msgs)})
}

// UpdateSession handles PATCH /v1/sessions/{id}.
//
// @Summary   Update a session's title or metadata
// @Tags      Sessions
// @Accept    json
// @Produce   json
// @Param     id       path      string                    true  "Session id"  format(uuid)
// @Param     request  body      dto.UpdateSessionRequest  true  "Fields to update"
// @Success   200      {object}  dto.SessionBrief
// @Failure   400      {object}  dto.ErrorResponse
// @Failure   401      {object}  dto.ErrorResponse
// @Failure   403      {object}  dto.ErrorResponse
// @Failure   404      {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions/{id} [patch]
func (h *Handlers) UpdateSession(ctx context.Context, c *app.RequestContext) {
	sess, ok := h.ownedSession(ctx, c)
	if !ok {
		return
	}
	var req dto.UpdateSessionRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	updated, err := h.Store.Sessions.Update(ctx, sess.ID, store.UpdateParams{Title: req.Title, Metadata: req.Metadata})
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.BriefFromDomain(updated))
}

// DeleteSession handles DELETE /v1/sessions/{id}.
//
// @Summary   Soft-delete a session
// @Tags      Sessions
// @Produce   json
// @Param     id   path      string  true  "Session id"  format(uuid)
// @Success   200  {object}  dto.DeleteResponse
// @Failure   401  {object}  dto.ErrorResponse
// @Failure   403  {object}  dto.ErrorResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/sessions/{id} [delete]
func (h *Handlers) DeleteSession(ctx context.Context, c *app.RequestContext) {
	sess, ok := h.ownedSession(ctx, c)
	if !ok {
		return
	}
	if err := h.Store.Sessions.SoftDelete(ctx, sess.ID); err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.DeleteResponse{ID: sess.ID.String(), Deleted: true})
}

func (h *Handlers) ownedSession(ctx context.Context, c *app.RequestContext) (domain.Session, bool) {
	user, _ := middleware.UserFrom(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.notFound(c, "session not found")
		return domain.Session{}, false
	}
	sess, err := h.Store.Sessions.Get(ctx, id)
	if err == store.ErrNotFound {
		h.notFound(c, "session not found")
		return domain.Session{}, false
	}
	if err != nil {
		h.serverError(c, err)
		return domain.Session{}, false
	}
	if sess.UserID != user.ID {
		c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "session belongs to another user"))
		return domain.Session{}, false
	}
	return sess, true
}

func encodeCursor(t time.Time) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano)))
}

func decodeCursor(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return t
}

// sessionTitle derives a session title from the first user message: at most max
// characters, cut on a character boundary. Slicing bytes instead would split a
// multi-byte character (every Vietnamese diacritic is 2-3 bytes) and produce
// invalid UTF-8, which Postgres rejects.
func sessionTitle(firstText string, max int) string {
	r := []rune(firstText)
	if len(r) <= max {
		return firstText
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
