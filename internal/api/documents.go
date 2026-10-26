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
// @Summary     Ask the agent about PDFs and images (streaming or buffered)
// @Description The body is an Anthropic Messages request. The last user message carries the files as `document` (PDF) or `image` content blocks with a base64 `source`, and the question as `text` blocks. Every file is read with docpipe (markdown and OCR text per page), and the agent answers the question with the documents in its context. The response is the same as `/v1/messages`: with `stream: true` (or `Accept: text/event-stream`) Anthropic SSE frames, preceded by a `document_parsed` extension event once the files are read (`ping` frames keep the stream open while they are); otherwise a single JSON message, plus `request_id` and `documents`.
// @Description The turn is stored in the session like any other (`metadata.session_id` continues one), so later `/v1/messages` calls on that session can ask about the same documents. The request, the files, what was read and the response are stored (`document_requests`, `document_files`).
// @Tags        Documents
// @Accept      json
// @Produce     json
// @Produce     text/event-stream
// @Param       request  body      dto.MessagesRequest  true  "Anthropic Messages request with document / image blocks"
// @Success     200      {object}  dto.DocumentMessageResponse
// @Success     202      {object}  dto.SteeredResponse
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Failure     403      {object}  dto.ErrorResponse
// @Failure     404      {object}  dto.ErrorResponse
// @Failure     413      {object}  dto.ErrorResponse
// @Failure     502      {object}  dto.ErrorResponse
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
	docs, err := last.Documents()
	if err != nil {
		h.badRequest(c, err.Error())
		return
	}
	if len(docs) == 0 {
		h.badRequest(c, "the last user message must contain at least one document or image block")
		return
	}
	if len(docs) > h.Docs.MaxFiles {
		h.badRequest(c, fmt.Sprintf("at most %d documents per request", h.Docs.MaxFiles))
		return
	}
	prompt := strings.TrimSpace(last.Text())
	if prompt == "" {
		h.badRequest(c, "the last user message must contain a text block with the question")
		return
	}
	files := make([]document.File, len(docs))
	for i, d := range docs {
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

	// The documents go to the agent as text; the session is titled from the
	// question.
	in, ok := h.turnInput(ctx, c, user, req, prompt, prompt)
	if !ok {
		return
	}
	rec := h.startDocumentRequest(ctx, c, user, domain.DocumentKindMessage, req.WithoutFileData(), files)
	if rec == nil {
		return
	}
	rec.SessionID = &in.Session.ID

	if wantsStream(c, req) {
		h.streamDocumentTurn(ctx, c, rec, in, files, prompt)
		return
	}

	results, err := h.parseAll(ctx, files)
	rec.Result = resultRecord(files, results)
	if err != nil {
		h.failDocumentRequest(ctx, c, rec, err)
		return
	}
	in.UserText = documentPrompt(files, results, prompt)
	out, err := h.Agent.Run(ctx, in, nil)
	h.linkMessages(rec, out)
	if err != nil && len(out.AssistantMessage.Blocks) == 0 {
		h.failDocumentRequest(ctx, c, rec, err)
		return
	}
	if out.Steered {
		resp := steeredResponse(in, out)
		rec.Status, rec.Response = domain.DocumentOK, resp
		h.finishDocumentRequest(ctx, rec)
		c.JSON(consts.StatusAccepted, resp)
		return
	}
	resp := dto.DocumentMessageResponse{MessageResponse: messageResponse(in, out), RequestID: rec.ID.String(), Documents: documentInfos(files, results)}
	rec.Status, rec.Response = domain.DocumentOK, resp
	if err != nil {
		rec.Status, rec.Error = domain.DocumentError, err.Error()
	}
	h.finishDocumentRequest(ctx, rec)
	c.JSON(consts.StatusOK, resp)
}

// streamDocumentTurn opens the stream first, so the client sees pings while
// the files are read, then streams the turn like /v1/messages.
func (h *Handlers) streamDocumentTurn(ctx context.Context, c *app.RequestContext, rec *domain.DocumentRequest, in agent.RunInput, files []document.File, prompt string) {
	w, stop := h.openStream(ctx, c)
	defer stop()

	results, err := h.parseAll(ctx, files)
	rec.Result = resultRecord(files, results)
	if err != nil {
		rec.Status, rec.Error = domain.DocumentError, err.Error()
		h.finishDocumentRequest(ctx, rec)
		w.Publish(errorFrame(documentErrorType(err), err.Error()))
		return
	}
	parsed, _ := json.Marshal(map[string]any{"type": "document_parsed", "request_id": rec.ID.String(), "documents": documentInfos(files, results)})
	w.Publish(&events.Frame{Event: "document_parsed", Data: parsed})

	in.UserText = documentPrompt(files, results, prompt)
	out, err := h.runStream(ctx, w, in)
	h.linkMessages(rec, out)
	rec.Status = domain.DocumentOK
	if out.Steered {
		rec.Response = steeredResponse(in, out)
	} else {
		rec.Response = dto.DocumentMessageResponse{MessageResponse: messageResponse(in, out), RequestID: rec.ID.String(), Documents: documentInfos(files, results)}
	}
	if err != nil {
		rec.Status, rec.Error = domain.DocumentError, err.Error()
	}
	h.finishDocumentRequest(ctx, rec)
}

// parseAll reads every file, in order. It fails on the first file that cannot
// be read at all; pages that fail inside a readable file are kept as errors.
func (h *Handlers) parseAll(ctx context.Context, files []document.File) ([]document.Result, error) {
	results := make([]document.Result, 0, len(files))
	for i, f := range files {
		res, err := h.Documents.Parse(ctx, f)
		results = append(results, res)
		if err == nil && len(res.Pages) > 0 && res.Failed() == len(res.Pages) {
			err = fmt.Errorf("%w: %s", errAllPagesFailed, res.Pages[0].Error)
		}
		if err != nil {
			return results, fmt.Errorf("document %d (%s): %w", i+1, displayName(f, i), err)
		}
	}
	return results, nil
}

// documentPrompt is the user message the agent receives: every document's
// pages (markdown and OCR text), then the question.
func documentPrompt(files []document.File, results []document.Result, prompt string) string {
	var b strings.Builder
	b.WriteString("<documents>\n")
	for i, res := range results {
		fmt.Fprintf(&b, "<document index=\"%d\" name=%q media_type=%q pages=\"%d\">\n", i+1, displayName(files[i], i), res.MediaType, len(res.Pages))
		for _, p := range res.Pages {
			fmt.Fprintf(&b, "<page number=\"%d\">\n", p.PageNo)
			if p.Error != "" {
				fmt.Fprintf(&b, "<error>this page could not be read: %s</error>\n", p.Error)
			} else {
				b.WriteString("<markdown>\n")
				b.WriteString(strings.TrimSpace(p.Markdown))
				b.WriteString("\n</markdown>\n<ocr_text>\n")
				b.WriteString(strings.TrimSpace(p.Text))
				b.WriteString("\n</ocr_text>\n")
			}
			b.WriteString("</page>\n")
		}
		b.WriteString("</document>\n")
	}
	b.WriteString("</documents>\n\n")
	b.WriteString("The documents above were read by OCR: <markdown> is the page laid out (headings, tables), <ocr_text> is the raw recognised text. Prefer <markdown>; check <ocr_text> where it looks garbled or incomplete.\n\n")
	b.WriteString(prompt)
	return b.String()
}

func displayName(f document.File, i int) string {
	if f.Name != "" {
		return f.Name
	}
	return fmt.Sprintf("document-%d", i+1)
}

func documentInfo(f document.File, res document.Result) dto.DocumentInfo {
	return dto.DocumentInfo{Name: f.Name, MediaType: res.MediaType, SizeBytes: len(f.Data), Pages: len(res.Pages), FailedPages: res.Failed()}
}

func documentInfos(files []document.File, results []document.Result) []dto.DocumentInfo {
	out := make([]dto.DocumentInfo, len(results))
	for i, res := range results {
		out[i] = documentInfo(files[i], res)
	}
	return out
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
