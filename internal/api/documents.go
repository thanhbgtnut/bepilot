package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/agentcore/agent"
	"github.com/thanhenti/bepilot/agentcore/agent/events"
	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/document"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/server/middleware"
)

// errAllPagesFailed is returned when the OCR engine could not read any page.
var errAllPagesFailed = errors.New("no page of the document could be read")

// ParseDocument handles POST /v1/documents/parse.
//
// @Summary     Read a PDF or image into markdown
// @Description Reads one uploaded file (PDF, JPEG, PNG or TIFF) with docpipe: a PDF is rendered page by page, every page is read by TurboOCR and, when a VLM is configured, re-read by it into markdown. Returns the markdown of every page and of the whole document. A page that could not be read carries an `error` and the rest still return; when no page can be read the response is `502`. The request, the file and the result are stored (`document_requests`, `document_files`).
// @Tags        Documents
// @Accept      multipart/form-data
// @Produce     json
// @Param       file  formData  file  true  "PDF or image (JPEG, PNG, TIFF)"
// @Success     200   {object}  dto.ParseDocumentResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     413   {object}  dto.ErrorResponse
// @Failure     502   {object}  dto.ErrorResponse
// @Failure     503   {object}  dto.ErrorResponse
// @Security    ApiKeyAuth
// @Router      /v1/documents/parse [post]
func (h *Handlers) ParseDocument(ctx context.Context, c *app.RequestContext) {
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

	rec := h.startDocumentRequest(ctx, c, user, domain.DocumentKindParse, map[string]any{
		"file": map[string]any{"name": file.Name, "media_type": file.MediaType, "size_bytes": len(data), "file_index": 0},
	}, []document.File{file})
	if rec == nil {
		return
	}

	res, err := h.Documents.Parse(ctx, file)
	if err == nil && len(res.Pages) > 0 && res.Failed() == len(res.Pages) {
		err = fmt.Errorf("%w: %s", errAllPagesFailed, res.Pages[0].Error)
	}
	if err != nil {
		rec.Result = resultRecord([]document.File{file}, []document.Result{res})
		h.failDocumentRequest(ctx, c, rec, err)
		return
	}

	resp := dto.ParseDocumentResponse{
		RequestID: rec.ID.String(),
		Document:  documentInfo(file, res),
		Markdown:  res.Markdown(),
		Pages:     make([]dto.DocumentPage, len(res.Pages)),
	}
	for i, p := range res.Pages {
		resp.Pages[i] = dto.DocumentPage{PageNo: p.PageNo, Markdown: p.Markdown, Error: p.Error}
	}
	rec.Status = domain.DocumentOK
	rec.Result = resultRecord([]document.File{file}, []document.Result{res})
	rec.Response = resp
	h.finishDocumentRequest(ctx, rec)
	c.JSON(consts.StatusOK, resp)
}

// DocumentMessages handles POST /v1/documents/messages.
//
// @Summary     Send files and a question in one request
// @Description The body is an Anthropic Messages request whose last user message carries the files as `document` (PDF) or `image` content blocks with a base64 `source`, and the question as `text` blocks. Every file is stored as a document (as `POST /v1/documents` does; the same file uploaded before is reused) and attached to the session (`metadata.session_id`, or a new session titled from the question). The files are read in the background.
// @Description With `stream: true` (or `Accept: text/event-stream`) the stream stays open while the files are read, sending `document_progress` events (and `ping` frames), then answers like `/v1/messages`: the agent gets the full text of every document of the session. Without streaming, the answer is `202` with the documents and the session as soon as the files are stored; ask on the session with `/v1/messages` once they are `ready` (a session whose documents are all ready already is answered at once, `200`). The documents stay attached: later questions on the session need not send them again.
// @Tags        Documents
// @Accept      json
// @Produce     json
// @Produce     text/event-stream
// @Param       request  body      dto.MessagesRequest  true  "Anthropic Messages request with document / image blocks"
// @Success     200      {object}  dto.MessageResponse
// @Success     202      {object}  dto.DocumentsAccepted
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Failure     403      {object}  dto.ErrorResponse
// @Failure     404      {object}  dto.ErrorResponse
// @Failure     413      {object}  dto.ErrorResponse
// @Failure     422      {object}  dto.ErrorResponse
// @Failure     503      {object}  dto.ErrorResponse
// @Security    ApiKeyAuth
// @Router      /v1/documents/messages [post]
func (h *Handlers) DocumentMessages(ctx context.Context, c *app.RequestContext) {
	user, ok := middleware.UserFrom(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "unauthenticated"))
		return
	}
	if !h.documentsEnabled(c) {
		return
	}
	var req dto.MessagesRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	last, ok := req.LastUserMessage()
	if !ok {
		h.badRequest(c, "messages must contain a user message")
		return
	}
	inputs, err := last.Documents()
	if err != nil {
		h.badRequest(c, err.Error())
		return
	}
	if len(inputs) == 0 {
		h.badRequest(c, "the last user message must contain at least one document or image block")
		return
	}
	if len(inputs) > h.Docs.MaxFiles {
		h.badRequest(c, fmt.Sprintf("at most %d documents per request", h.Docs.MaxFiles))
		return
	}
	prompt := strings.TrimSpace(last.Text())
	if prompt == "" {
		h.badRequest(c, "the last user message must contain a text block with the question")
		return
	}
	files := make([]document.File, len(inputs))
	for i, d := range inputs {
		files[i] = document.File{Name: d.Name, MediaType: d.MediaType, Data: d.Data}
		if int64(len(d.Data)) > h.maxFileBytes() {
			h.tooLarge(c, fmt.Sprintf("document %d is larger than %d MB", i+1, h.Docs.MaxFileMB))
			return
		}
		if msg := h.checkFile(files[i]); msg != "" {
			h.badRequest(c, fmt.Sprintf("document %d: %s", i+1, msg))
			return
		}
	}

	// The question is the user message; the documents reach the agent
	// through the session.
	in, ok := h.turnInput(ctx, c, user, req, prompt, prompt)
	if !ok {
		return
	}
	stored := make([]dto.Document, 0, len(files))
	ids := make([]uuid.UUID, 0, len(files))
	for _, f := range files {
		d, _, err := h.storeDocument(ctx, user, f)
		if err != nil {
			h.serverError(c, err)
			return
		}
		stored = append(stored, dto.DocumentFromDomain(d))
		ids = append(ids, d.ID)
	}
	if err := h.Store.Library.Attach(ctx, in.Session.ID, ids); err != nil {
		h.serverError(c, err)
		return
	}
	docs, ok := h.sessionDocuments(ctx, c, user, in.Session, dto.MessagesRequest{})
	if !ok {
		return
	}

	stream := wantsStream(c, req)
	if !stream && documentsReady(docs) != nil {
		c.JSON(consts.StatusAccepted, dto.DocumentsAccepted{Type: "documents_accepted", Documents: stored, Session: dto.BriefFromDomain(in.Session)})
		return
	}
	h.documentTurn(ctx, c, user, in, docs, stream)
}

func documentInfo(f document.File, res document.Result) dto.DocumentInfo {
	return dto.DocumentInfo{Name: f.Name, MediaType: res.MediaType, SizeBytes: len(f.Data), Pages: len(res.Pages), FailedPages: res.Failed()}
}

// resultRecord is what is stored as document_requests.result: per file, its
// detected type and every page's markdown, OCR text and error.
func resultRecord(files []document.File, results []document.Result) []map[string]any {
	out := make([]map[string]any, len(results))
	for i, res := range results {
		pages := make([]dto.DocumentPage, len(res.Pages))
		for j, p := range res.Pages {
			pages[j] = dto.DocumentPage{PageNo: p.PageNo, Markdown: p.Markdown, Text: p.Text, Error: p.Error}
		}
		out[i] = map[string]any{"file_index": i, "name": files[i].Name, "media_type": res.MediaType, "pages": pages}
	}
	return out
}

// --- request records ------------------------------------------------------

// startDocumentRequest stores the request and its files before any work is
// done. On failure it writes a 500 and returns nil.
func (h *Handlers) startDocumentRequest(ctx context.Context, c *app.RequestContext, user domain.User, kind string, request map[string]any, files []document.File) *domain.DocumentRequest {
	rec := &domain.DocumentRequest{UserID: user.ID, Kind: kind, Request: request}
	rows := make([]domain.DocumentFile, len(files))
	for i, f := range files {
		rows[i] = domain.DocumentFile{Name: f.Name, MediaType: f.MediaType, Data: f.Data}
	}
	if err := h.Store.Documents.Create(ctx, rec, rows); err != nil {
		h.serverError(c, err)
		return nil
	}
	return rec
}

// finishDocumentRequest records how the request ended. It is detached from
// the request so a client that hung up still leaves a complete record.
func (h *Handlers) finishDocumentRequest(ctx context.Context, rec *domain.DocumentRequest) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := h.Store.Documents.Finish(fctx, *rec); err != nil {
		h.Log.Error("record document request failed", "request", rec.ID, "err", err)
	}
}

// failDocumentRequest records the error and answers with it.
func (h *Handlers) failDocumentRequest(ctx context.Context, c *app.RequestContext, rec *domain.DocumentRequest, err error) {
	rec.Status, rec.Error = domain.DocumentError, err.Error()
	h.finishDocumentRequest(ctx, rec)
	kind := documentErrorType(err)
	status := consts.StatusInternalServerError
	switch kind {
	case "invalid_request_error":
		status = consts.StatusBadRequest
	case "ocr_error":
		status = consts.StatusBadGateway
	}
	if status == consts.StatusInternalServerError {
		h.Log.Error("document request failed", "request", rec.ID, "err", err)
	}
	c.JSON(status, dto.NewError(kind, err.Error()))
}

func (h *Handlers) linkMessages(rec *domain.DocumentRequest, out agent.RunOutput) {
	if out.UserMessage.ID != uuid.Nil {
		id := out.UserMessage.ID
		rec.UserMessageID = &id
	}
	if out.AssistantMessage.ID != uuid.Nil {
		id := out.AssistantMessage.ID
		rec.AssistantMessageID = &id
	}
}

// documentErrorType classifies a document failure as an Anthropic error type.
func documentErrorType(err error) string {
	switch {
	case errors.Is(err, errAllPagesFailed):
		return "ocr_error"
	case errors.Is(err, document.ErrUnsupported), errors.Is(err, document.ErrUnreadable):
		return "invalid_request_error"
	default:
		return "api_error"
	}
}

func errorFrame(kind, msg string) *events.Frame {
	data, _ := json.Marshal(dto.NewError(kind, msg))
	return &events.Frame{Event: "error", Data: data}
}

// --- checks ---------------------------------------------------------------

func (h *Handlers) documentsEnabled(c *app.RequestContext) bool {
	if h.Documents == nil {
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", "document endpoints are not enabled on this server (documents.enabled)"))
		return false
	}
	return true
}

func (h *Handlers) maxFileBytes() int64 { return int64(h.Docs.MaxFileMB) << 20 }

// checkFile returns why f cannot be read, or "". Size is checked separately
// (413).
func (h *Handlers) checkFile(f document.File) string {
	if document.Detect(f.Data) == "" {
		return document.ErrUnsupported.Error()
	}
	return ""
}

func (h *Handlers) tooLarge(c *app.RequestContext, msg string) {
	c.JSON(consts.StatusRequestEntityTooLarge, dto.NewError("request_too_large", msg))
}
