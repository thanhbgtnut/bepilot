package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/document"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/server/middleware"
	"github.com/thanhenti/bepilot/internal/store"
)

// UploadDocument handles POST /v1/documents.
//
// @Summary     Upload a document to be read in the background
// @Description Stores one file (PDF, JPEG, PNG or TIFF) and answers at once with `202` and the document in status `pending`; the file is read in the background, page by page. Poll `GET /v1/documents/{id}` for the progress (`pages_done` / `pages_total`) until the status is `ready` (or `failed`). Every page is read by OCR and, when a vision model is configured, by it as well; numbers the two readings disagree on are put back to the vision model, replaced only when it is sure, and otherwise noted as unverified in the page.
// @Description The same file uploaded again by the same user returns the existing document (`duplicate: true`, status `200`) without reading it again; one that had failed is queued again. With `session_id`, the document is attached to that session: every question asked on the session then gets the full text of its documents.
// @Tags        Documents
// @Accept      multipart/form-data
// @Produce     json
// @Param       file        formData  file    true   "PDF or image (JPEG, PNG, TIFF)"
// @Param       session_id  formData  string  false  "Session to attach the document to"
// @Success     200         {object}  dto.UploadDocumentResponse  "the same file was already uploaded"
// @Success     202         {object}  dto.UploadDocumentResponse
// @Failure     400         {object}  dto.ErrorResponse
// @Failure     401         {object}  dto.ErrorResponse
// @Failure     403         {object}  dto.ErrorResponse
// @Failure     404         {object}  dto.ErrorResponse
// @Failure     413         {object}  dto.ErrorResponse
// @Failure     503         {object}  dto.ErrorResponse
// @Security    ApiKeyAuth
// @Router      /v1/documents [post]
func (h *Handlers) UploadDocument(ctx context.Context, c *app.RequestContext) {
	user, ok := middleware.UserFrom(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "unauthenticated"))
		return
	}
	if !h.documentsEnabled(c) {
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		h.badRequest(c, `multipart field "file" is required`)
		return
	}
	if fh.Size > h.maxFileBytes() {
		h.tooLarge(c, fmt.Sprintf("file is larger than %d MB", h.Docs.MaxFileMB))
		return
	}
	src, err := fh.Open()
	if err != nil {
		h.badRequest(c, "cannot read the uploaded file: "+err.Error())
		return
	}
	data, err := io.ReadAll(io.LimitReader(src, h.maxFileBytes()+1))
	src.Close()
	if err != nil {
		h.badRequest(c, "cannot read the uploaded file: "+err.Error())
		return
	}
	if int64(len(data)) > h.maxFileBytes() {
		h.tooLarge(c, fmt.Sprintf("file is larger than %d MB", h.Docs.MaxFileMB))
		return
	}
	file := document.File{Name: fh.Filename, MediaType: fh.Header.Get("Content-Type"), Data: data}
	if msg := h.checkFile(file); msg != "" {
		h.badRequest(c, msg)
		return
	}

	var sess *domain.Session
	if sid := strings.TrimSpace(string(c.FormValue("session_id"))); sid != "" {
		s, ok := h.userSession(ctx, c, user, sid)
		if !ok {
			return
		}
		sess = &s
	}

	d, created, err := h.storeDocument(ctx, user, file)
	if err != nil {
		h.serverError(c, err)
		return
	}
	resp := dto.UploadDocumentResponse{Document: dto.DocumentFromDomain(d), Duplicate: !created}
	if sess != nil {
		if err := h.Store.Library.Attach(ctx, sess.ID, []uuid.UUID{d.ID}); err != nil {
			h.serverError(c, err)
			return
		}
		brief := dto.BriefFromDomain(*sess)
		resp.Session = &brief
	}
	status := consts.StatusAccepted
	if !created && d.Status != domain.DocumentPending {
		status = consts.StatusOK
	}
	c.JSON(status, resp)
}

// storeDocument stores an uploaded file (or finds the same one) and wakes a
// worker when it has to be read.
func (h *Handlers) storeDocument(ctx context.Context, user domain.User, f document.File) (domain.Document, bool, error) {
	d := domain.Document{UserID: user.ID, Name: f.Name, MediaType: document.Detect(f.Data), Data: f.Data}
	created, err := h.Store.Library.Create(ctx, &d)
	if err != nil {
		return d, false, err
	}
	if d.Status == domain.DocumentPending && h.Jobs != nil {
		h.Jobs.Notify()
	}
	return d, created, nil
}

// ListDocuments handles GET /v1/documents.
//
// @Summary   List the authenticated user's documents
// @Tags      Documents
// @Produce   json
// @Param     limit   query     int     false  "Page size (1-100)"  default(30)
// @Param     cursor  query     string  false  "Opaque cursor from a previous response's next_cursor"
// @Success   200     {object}  dto.DocumentList
// @Failure   401     {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents [get]
func (h *Handlers) ListDocuments(ctx context.Context, c *app.RequestContext) {
	user, _ := middleware.UserFrom(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	docs, err := h.Store.Library.ListByUser(ctx, user.ID, decodeCursor(c.Query("cursor")), limit+1)
	if err != nil {
		h.serverError(c, err)
		return
	}
	resp := dto.DocumentList{Data: []dto.Document{}}
	if len(docs) > limit {
		resp.HasMore = true
		resp.NextCursor = encodeCursor(docs[limit-1].CreatedAt)
		docs = docs[:limit]
	}
	for _, d := range docs {
		resp.Data = append(resp.Data, dto.DocumentFromDomain(d))
	}
	c.JSON(consts.StatusOK, resp)
}

// GetDocument handles GET /v1/documents/{id}.
//
// @Summary   Get a document's status and progress
// @Tags      Documents
// @Produce   json
// @Param     id   path      string  true  "Document id"  format(uuid)
// @Success   200  {object}  dto.Document
// @Failure   401  {object}  dto.ErrorResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id} [get]
func (h *Handlers) GetDocument(ctx context.Context, c *app.RequestContext) {
	d, ok := h.ownedDocument(ctx, c)
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, dto.DocumentFromDomain(d))
}

// ListDocumentPages handles GET /v1/documents/{id}/pages.
//
// @Summary   Get what was read from a document's pages
// @Description Returns the pages read so far (all of them once the document is ready): the markdown given to the agent, the OCR text and the number check of each page.
// @Tags      Documents
// @Produce   json
// @Param     id    path      string  true   "Document id"  format(uuid)
// @Param     from  query     int     false  "First page (1-based)"
// @Param     to    query     int     false  "Last page"
// @Success   200   {object}  dto.DocumentPages
// @Failure   401   {object}  dto.ErrorResponse
// @Failure   404   {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id}/pages [get]
func (h *Handlers) ListDocumentPages(ctx context.Context, c *app.RequestContext) {
	d, ok := h.ownedDocument(ctx, c)
	if !ok {
		return
	}
	from, _ := strconv.Atoi(c.Query("from"))
	to, _ := strconv.Atoi(c.Query("to"))
	pages, err := h.Store.Library.Pages(ctx, d.ID, from, to)
	if err != nil {
		h.serverError(c, err)
		return
	}
	resp := dto.DocumentPages{Document: dto.DocumentFromDomain(d), Pages: make([]dto.StoredPage, len(pages))}
	for i, p := range pages {
		resp.Pages[i] = dto.StoredPageFromDomain(p)
	}
	c.JSON(consts.StatusOK, resp)
}

// DeleteDocument handles DELETE /v1/documents/{id}.
//
// @Summary   Delete a document
// @Description Deletes the document, its pages and its links to sessions. A document being read stops being read.
// @Tags      Documents
// @Param     id   path  string  true  "Document id"  format(uuid)
// @Success   204
// @Failure   401  {object}  dto.ErrorResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/documents/{id} [delete]
func (h *Handlers) DeleteDocument(ctx context.Context, c *app.RequestContext) {
	d, ok := h.ownedDocument(ctx, c)
	if !ok {
		return
	}
	if err := h.Store.Library.Delete(ctx, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		h.serverError(c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// ownedDocument loads the document named by the :id path parameter. Another
// user's document is answered as not found.
func (h *Handlers) ownedDocument(ctx context.Context, c *app.RequestContext) (domain.Document, bool) {
	user, _ := middleware.UserFrom(c)
	d, err := h.userDocument(ctx, user, c.Param("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		h.notFound(c, "document not found")
		return d, false
	case err != nil:
		h.serverError(c, err)
		return d, false
	}
	return d, true
}

// userDocument returns the user's document with id, or store.ErrNotFound.
func (h *Handlers) userDocument(ctx context.Context, user domain.User, id string) (domain.Document, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return domain.Document{}, store.ErrNotFound
	}
	d, err := h.Store.Library.Get(ctx, uid)
	if err != nil {
		return d, err
	}
	if d.UserID != user.ID {
		return domain.Document{}, store.ErrNotFound
	}
	return d, nil
}

// userSession loads a session of the user by id; on failure it writes the
// error response.
func (h *Handlers) userSession(ctx context.Context, c *app.RequestContext, user domain.User, id string) (domain.Session, bool) {
	sid, err := uuid.Parse(id)
	if err != nil {
		h.notFound(c, "session not found")
		return domain.Session{}, false
	}
	sess, err := h.Store.Sessions.Get(ctx, sid)
	switch {
	case errors.Is(err, store.ErrNotFound):
		h.notFound(c, "session not found")
		return sess, false
	case err != nil:
		h.serverError(c, err)
		return sess, false
	case sess.UserID != user.ID:
		c.JSON(consts.StatusForbidden, dto.NewError("permission_error", "session belongs to another user"))
		return sess, false
	}
	return sess, true
}
