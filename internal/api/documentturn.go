package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/thanhenti/bepilot/internal/server/sse"
	"github.com/thanhenti/bepilot/internal/store"
)

// A turn on a session with documents gets the full text of every page of
// every document of the session, through the request-level system prompt
// (agent.RunInput.RequestSystem): the agent stays as it is, the documents
// never enter the stored history, and the history budget never cuts them.

// documentInstructions go before the documents in the system prompt.
const documentInstructions = `The documents below are attached to this conversation; their full text follows, page by page. ` +
	`Answer from them. Cite the page of every figure or statement you take from them, as (page N), with the document name when there are several. ` +
	`A page may end with an <!-- unverified numbers: ... --> note: those numbers were read differently by two readers and could not be confirmed; ` +
	`when you use one, say that it may be misread. A page marked as unreadable has no text. ` +
	`If the documents do not contain the answer, say so instead of guessing.`

// systemAllowance is the room kept for the agent's own system prompt (tools,
// skills, environment) when a turn is checked against the context window.
const systemAllowance = 6000

// turnError is a document turn that cannot run, with the HTTP status and
// error type to answer.
type turnError struct {
	status int
	kind   string
	msg    string
}

func (e *turnError) Error() string { return e.msg }

// sessionDocuments attaches the documents named by metadata.document_ids
// to the session and returns every document of the session. On failure it
// writes the error response and returns false.
func (h *Handlers) sessionDocuments(ctx context.Context, c *app.RequestContext, user domain.User, sess domain.Session, req dto.MessagesRequest) ([]domain.Document, bool) {
	var ids []uuid.UUID
	if req.Metadata != nil {
		for _, raw := range req.Metadata.DocumentIDs {
			d, err := h.userDocument(ctx, user, raw)
			if errors.Is(err, store.ErrNotFound) {
				h.notFound(c, fmt.Sprintf("document %s not found", raw))
				return nil, false
			}
			if err != nil {
				h.serverError(c, err)
				return nil, false
			}
			ids = append(ids, d.ID)
		}
	}
	if len(ids) > 0 {
		if err := h.Store.Library.Attach(ctx, sess.ID, ids); err != nil {
			h.serverError(c, err)
			return nil, false
		}
	}
	docs, err := h.Store.Library.SessionDocuments(ctx, sess.ID)
	if err != nil {
		h.serverError(c, err)
		return nil, false
	}
	return docs, true
}

// documentTurn runs a turn on a session that has documents. Streamed, it
// waits for documents still being read (sending document_progress events);
// buffered, it answers 409 while they are.
func (h *Handlers) documentTurn(ctx context.Context, c *app.RequestContext, user domain.User, in agent.RunInput, docs []domain.Document, stream bool) {
	rec := h.startDocumentRequest(ctx, c, user, domain.DocumentKindMessage, map[string]any{
		"session_id": in.Session.ID.String(), "document_ids": documentIDs(docs), "question": in.UserText,
	}, nil)
	if rec == nil {
		return
	}
	rec.SessionID = &in.Session.ID

	if stream {
		w, stop := h.openStream(ctx, c)
		defer stop()
		docs, err := h.waitDocuments(ctx, w, in.Session.ID, docs)
		if err == nil {
			err = h.withDocuments(ctx, &in, docs)
		}
		if err != nil {
			rec.Status, rec.Error = domain.DocumentError, err.Error()
			h.finishDocumentRequest(ctx, rec)
			w.Publish(errorFrame(turnErrorKind(err), err.Error()))
			return
		}
		out, err := h.runStream(ctx, w, in)
		h.finishTurnRecord(ctx, rec, in, out, err)
		return
	}

	if err := documentsReady(docs); err != nil {
		h.failTurn(ctx, c, rec, err)
		return
	}
	if err := h.withDocuments(ctx, &in, docs); err != nil {
		h.failTurn(ctx, c, rec, err)
		return
	}
	out, err := h.Agent.Run(ctx, in, nil)
	h.finishTurnRecord(ctx, rec, in, out, err)
	if err != nil && len(out.AssistantMessage.Blocks) == 0 {
		h.serverError(c, err)
		return
	}
	if out.Steered {
		c.JSON(consts.StatusAccepted, steeredResponse(in, out))
		return
	}
	c.JSON(consts.StatusOK, messageResponse(in, out))
}

func (h *Handlers) finishTurnRecord(ctx context.Context, rec *domain.DocumentRequest, in agent.RunInput, out agent.RunOutput, err error) {
	h.linkMessages(rec, out)
	rec.Status = domain.DocumentOK
	if out.Steered {
		rec.Response = steeredResponse(in, out)
	} else {
		rec.Response = messageResponse(in, out)
	}
	if err != nil {
		rec.Status, rec.Error = domain.DocumentError, err.Error()
	}
	h.finishDocumentRequest(ctx, rec)
}

func (h *Handlers) failTurn(ctx context.Context, c *app.RequestContext, rec *domain.DocumentRequest, err error) {
	rec.Status, rec.Error = domain.DocumentError, err.Error()
	h.finishDocumentRequest(ctx, rec)
	var te *turnError
	if errors.As(err, &te) {
		c.JSON(te.status, dto.NewError(te.kind, te.msg))
		return
	}
	h.serverError(c, err)
}

func turnErrorKind(err error) string {
	var te *turnError
	if errors.As(err, &te) {
		return te.kind
	}
	return "api_error"
}

// documentsReady returns a turnError when a document is not ready: 409
// while one is being read, 422 when one failed.
func documentsReady(docs []domain.Document) error {
	var waiting []string
	for _, d := range docs {
		switch d.Status {
		case domain.DocumentReady:
		case domain.DocumentFailed:
			return &turnError{consts.StatusUnprocessableEntity, "document_error",
				fmt.Sprintf("document %s (%s) could not be read: %s", d.ID, d.Name, d.Error)}
		default:
			waiting = append(waiting, fmt.Sprintf("%s (%s, %d/%d pages)", d.ID, d.Status, d.PagesDone, d.PagesTotal))
		}
	}
	if len(waiting) > 0 {
		return &turnError{consts.StatusConflict, "document_not_ready",
			"documents of this session are still being read: " + strings.Join(waiting, ", ") + "; poll GET /v1/documents/{id} or ask with stream: true to wait"}
	}
	return nil
}

// waitDocuments waits until every document of the session is ready,
// sending a document_progress event whenever the progress moves.
func (h *Handlers) waitDocuments(ctx context.Context, w *sse.Writer, sessionID uuid.UUID, docs []domain.Document) ([]domain.Document, error) {
	deadline := time.Now().Add(h.Docs.WaitTimeout)
	last := ""
	for {
		err := documentsReady(docs)
		var te *turnError
		if err == nil || !errors.As(err, &te) || te.status != consts.StatusConflict {
			return docs, err
		}
		if key := progressKey(docs); key != last {
			last = key
			ev := dto.DocumentProgressEvent{Type: "document_progress"}
			for _, d := range docs {
				ev.Documents = append(ev.Documents, dto.DocumentFromDomain(d))
			}
			data, _ := json.Marshal(ev)
			w.Publish(&events.Frame{Event: "document_progress", Data: data})
		}
		if h.Docs.WaitTimeout > 0 && time.Now().After(deadline) {
			te.kind, te.msg = "timeout_error", fmt.Sprintf("documents not ready after %s: %s", h.Docs.WaitTimeout, te.msg)
			return docs, te
		}
		select {
		case <-ctx.Done():
			return docs, ctx.Err()
		case <-time.After(time.Second):
		}
		next, err := h.Store.Library.SessionDocuments(ctx, sessionID)
		if err != nil {
			return docs, err
		}
		docs = next
	}
}

func progressKey(docs []domain.Document) string {
	var b strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&b, "%s:%s:%d/%d;", d.ID, d.Status, d.PagesDone, d.PagesTotal)
	}
	return b.String()
}

// withDocuments puts the full text of docs into the turn's system prompt
// and checks that the turn fits the model's context window. A turn with
// documents runs at temperature 0 unless the request set one.
func (h *Handlers) withDocuments(ctx context.Context, in *agent.RunInput, docs []domain.Document) error {
	var b strings.Builder
	b.WriteString(documentInstructions)
	b.WriteString("\n\n<documents>\n")
	for _, d := range docs {
		pages, err := h.Store.Library.Pages(ctx, d.ID, 0, 0)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "<document id=%q name=%q pages=\"%d\">\n", d.ID.String(), displayDocName(d), d.PagesTotal)
		for _, p := range pages {
			if p.Error != "" {
				fmt.Fprintf(&b, "<page number=\"%d\" unreadable=\"true\"></page>\n", p.PageNo)
				continue
			}
			fmt.Fprintf(&b, "<page number=\"%d\">\n%s\n</page>\n", p.PageNo, strings.TrimSpace(p.Markdown))
		}
		b.WriteString("</document>\n")
	}
	b.WriteString("</documents>")
	block := b.String()

	if window := h.Docs.ContextWindowFor(in.Model); window > 0 {
		history := in.HistoryTokenBudget
		if history <= 0 {
			history = h.Agentcfg.HistoryTokenBudget
		}
		maxTokens := in.MaxTokens
		if maxTokens <= 0 {
			maxTokens = h.LLM.MaxTokens
		}
		docTokens := document.EstimateTokens(block)
		need := systemAllowance + document.EstimateTokens(in.RequestSystem) + document.EstimateTokens(in.UserText) + docTokens + history + maxTokens
		if need > window {
			return &turnError{consts.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf(
				"the documents of this session take about %d tokens; with the conversation and the answer the turn needs about %d, more than the %d tokens model %s takes",
				docTokens, need, window, in.Model)}
		}
	}

	in.RequestSystem = strings.TrimSpace(in.RequestSystem + "\n\n" + block)
	if in.Temperature == nil {
		zero := float32(0)
		in.Temperature = &zero
	}
	return nil
}

func displayDocName(d domain.Document) string {
	if d.Name != "" {
		return d.Name
	}
	return d.ID.String()
}

func documentIDs(docs []domain.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID.String()
	}
	return out
}
