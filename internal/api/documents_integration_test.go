package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
)

// fakeOCR answers TurboOCR's /ocr/raw with docpipe's sample response.
func fakeOCR(t *testing.T) *httptest.Server {
	t.Helper()
	sample, err := os.ReadFile("../../docpipe/parser/turboocr/testdata/output_example.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write(sample)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func samplePDF() []byte {
	return pdftest.Build([]pdftest.Page{{"PAGE ONE"}, {"PAGE TWO"}}, pdftest.Options{})
}

// documentRow is what a test reads back from document_requests.
type documentRow struct {
	Kind, Status, Error             string
	Request, Result, Response       []byte
	SessionID, UserMsgID, AsstMsgID *uuid.UUID
	Files                           int
	FileSize                        int64
	FileSHA                         string
}

func (e *testEnv) documentRow(t *testing.T, id string) documentRow {
	t.Helper()
	var r documentRow
	err := e.store.Pool.QueryRow(context.Background(), `
		SELECT kind, status, error, request, result, response, session_id, user_message_id, assistant_message_id,
			(SELECT count(*) FROM document_files f WHERE f.request_id = d.id),
			(SELECT coalesce(sum(size_bytes), 0) FROM document_files f WHERE f.request_id = d.id),
			(SELECT coalesce(min(sha256), '') FROM document_files f WHERE f.request_id = d.id)
		FROM document_requests d WHERE id = $1`, id,
	).Scan(&r.Kind, &r.Status, &r.Error, &r.Request, &r.Result, &r.Response, &r.SessionID, &r.UserMsgID, &r.AsstMsgID, &r.Files, &r.FileSize, &r.FileSHA)
	if err != nil {
		t.Fatalf("read document request %s: %v", id, err)
	}
	return r
}

func TestParseDocumentReturnsMarkdownAndStoresRequest(t *testing.T) {
	e := setup(t)
	pdf := samplePDF()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "two-pages.pdf")
	fw.Write(pdf)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, "http://"+e.addr+"/v1/documents/parse", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("x-api-key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		RequestID string `json:"request_id"`
		Document  struct {
			Name      string `json:"name"`
			MediaType string `json:"media_type"`
			Pages     int    `json:"pages"`
		} `json:"document"`
		Markdown string `json:"markdown"`
		Pages    []struct {
			PageNo   int    `json:"page_no"`
			Markdown string `json:"markdown"`
		} `json:"pages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Document.Name != "two-pages.pdf" || out.Document.MediaType != "application/pdf" || out.Document.Pages != 2 || len(out.Pages) != 2 {
		t.Fatalf("document = %+v, %d pages", out.Document, len(out.Pages))
	}
	if strings.TrimSpace(out.Markdown) == "" || out.Pages[0].Markdown == "" {
		t.Fatal("no markdown returned")
	}

	row := e.documentRow(t, out.RequestID)
	if row.Kind != "parse" || row.Status != "ok" || row.Files != 1 || row.FileSize != int64(len(pdf)) || len(row.FileSHA) != 64 {
		t.Fatalf("stored row = %+v", row)
	}
	if !bytes.Contains(row.Response, []byte(out.RequestID)) || !bytes.Contains(row.Result, []byte(`"text"`)) {
		t.Fatal("stored response/result is incomplete")
	}
}

func documentRequest(stream bool, prompt string, data []byte) map[string]any {
	content := []map[string]any{
		{"type": "document", "title": "two-pages.pdf", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": base64.StdEncoding.EncodeToString(data)}},
	}
	if prompt != "" {
		content = append(content, map[string]any{"type": "text", "text": prompt})
	}
	return map[string]any{
		"model": "bepilot-fake-1", "max_tokens": 512, "stream": stream,
		"messages": []map[string]any{{"role": "user", "content": content}},
	}
}

// docStatus is the part of a document the tests read.
type docStatus struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	PagesTotal int    `json:"pages_total"`
	PagesDone  int    `json:"pages_done"`
	TokenCount int    `json:"token_count"`
	Duplicate  bool   `json:"duplicate"`
	Session    *struct {
		ID string `json:"id"`
	} `json:"session"`
}

// upload posts a file to /v1/documents, with a session when sessionID is set.
func (e *testEnv) upload(t *testing.T, name string, data []byte, sessionID string) (int, docStatus) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	if sessionID != "" {
		mw.WriteField("session_id", sessionID)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, "http://"+e.addr+"/v1/documents", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("x-api-key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d docStatus
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, d
}

// waitDocument polls the document until it is no longer pending or
// processing.
func (e *testEnv) waitDocument(t *testing.T, id string) docStatus {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp := e.do(t, http.MethodGet, "/v1/documents/"+id, nil)
		var d docStatus
		json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if d.Status == "ready" || d.Status == "failed" {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("document %s still %s (%d/%d pages)", id, d.Status, d.PagesDone, d.PagesTotal)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUploadDocumentIsReadInBackground(t *testing.T) {
	e := setup(t)
	status, d := e.upload(t, "two-pages.pdf", samplePDF(), "")
	if status != http.StatusAccepted || d.Status != "pending" || d.ID == "" {
		t.Fatalf("upload: status %d, %+v", status, d)
	}
	d = e.waitDocument(t, d.ID)
	if d.Status != "ready" || d.PagesTotal != 2 || d.PagesDone != 2 || d.TokenCount == 0 {
		t.Fatalf("document = %+v", d)
	}

	resp := e.do(t, http.MethodGet, "/v1/documents/"+d.ID+"/pages?from=2", nil)
	var pages struct {
		Pages []struct {
			PageNo   int    `json:"page_no"`
			Markdown string `json:"markdown"`
			OCRText  string `json:"ocr_text"`
		} `json:"pages"`
	}
	json.NewDecoder(resp.Body).Decode(&pages)
	resp.Body.Close()
	if len(pages.Pages) != 1 || pages.Pages[0].PageNo != 2 || pages.Pages[0].Markdown == "" || pages.Pages[0].OCRText == "" {
		t.Fatalf("pages = %+v", pages)
	}

	// The same file again: the document already read, not a new one.
	status, again := e.upload(t, "copy.pdf", samplePDF(), "")
	if status != http.StatusOK || !again.Duplicate || again.ID != d.ID || again.Status != "ready" {
		t.Fatalf("re-upload: status %d, %+v", status, again)
	}

	resp = e.do(t, http.MethodDelete, "/v1/documents/"+d.ID, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}
	resp = e.do(t, http.MethodGet, "/v1/documents/"+d.ID, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status %d", resp.StatusCode)
	}
}

func messageRequest(stream bool, text string, metadata map[string]any) map[string]any {
	return map[string]any{
		"model": "bepilot-fake-1", "max_tokens": 512, "stream": stream, "metadata": metadata,
		"messages": []map[string]any{{"role": "user", "content": text}},
	}
}

// Every turn of a session gets its documents: the first names them, the
// next one does not, and neither stores them in the history.
func TestMessagesAnswerFromSessionDocuments(t *testing.T) {
	e := setup(t)
	_, d := e.upload(t, "two-pages.pdf", samplePDF(), "")
	e.waitDocument(t, d.ID)

	resp := e.do(t, http.MethodPost, "/v1/messages", messageRequest(false, "What is on page two?", map[string]any{"document_ids": []string{d.ID}}))
	var first struct {
		Content []any `json:"content"`
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	json.NewDecoder(resp.Body).Decode(&first)
	resp.Body.Close()
	if len(first.Content) == 0 || first.Session.ID == "" {
		t.Fatalf("first turn = %+v", first)
	}

	resp = e.do(t, http.MethodPost, "/v1/messages", messageRequest(false, "And on page one?", map[string]any{"session_id": first.Session.ID}))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("second turn: status %d", resp.StatusCode)
	}

	rows, err := e.store.Pool.Query(context.Background(), `SELECT request, status, user_message_id FROM document_requests
		WHERE kind = 'message' AND session_id = $1 ORDER BY created_at`, first.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var req []byte
		var status string
		var userMsg *uuid.UUID
		if err := rows.Scan(&req, &status, &userMsg); err != nil {
			t.Fatal(err)
		}
		n++
		if status != "ok" || userMsg == nil || !bytes.Contains(req, []byte(d.ID)) {
			t.Fatalf("turn %d record: status %q, request %s", n, status, req)
		}
		var text string
		e.store.Pool.QueryRow(context.Background(), `SELECT text FROM content_blocks WHERE message_id = $1 AND type = 'text'`, *userMsg).Scan(&text)
		if strings.Contains(text, "<documents>") {
			t.Fatalf("the documents were stored in the history: %q", text)
		}
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("%d turn records, want 2", n)
	}
}

// insertDocument stores a document in a given state that no worker takes.
func (e *testEnv) insertDocument(t *testing.T, status string) string {
	t.Helper()
	var id string
	err := e.store.Pool.QueryRow(context.Background(), `INSERT INTO library_documents (user_id, name, size_bytes, sha256, data, status, error, lease_token, locked_until)
		SELECT user_id, 'held.pdf', 1, md5(random()::text), '\x00', $1, CASE WHEN $1 = 'failed' THEN 'broken file' ELSE '' END,
			gen_random_uuid(), now() + interval '1 hour'
		FROM api_keys LIMIT 1 RETURNING id`, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMessagesRejectDocumentsNotReady(t *testing.T) {
	e := setup(t)
	for status, want := range map[string]int{"processing": http.StatusConflict, "failed": http.StatusUnprocessableEntity} {
		id := e.insertDocument(t, status)
		resp := e.do(t, http.MethodPost, "/v1/messages", messageRequest(false, "hi", map[string]any{"document_ids": []string{id}}))
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s document: status %d, want %d", status, resp.StatusCode, want)
		}
	}
	resp := e.do(t, http.MethodPost, "/v1/messages", messageRequest(false, "hi", map[string]any{"document_ids": []string{uuid.NewString()}}))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown document: status %d, want 404", resp.StatusCode)
	}
}

// sseEvents reads the event names of a stream (pings left out).
func sseEvents(t *testing.T, resp *http.Response) []string {
	t.Helper()
	var names []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if ev, ok := strings.CutPrefix(sc.Text(), "event:"); ok && strings.TrimSpace(ev) != "ping" {
			names = append(names, strings.TrimSpace(ev))
		}
	}
	return names
}

func TestDocumentMessagesBufferedAcceptsThenAnswers(t *testing.T) {
	e := setup(t)
	resp := e.do(t, http.MethodPost, "/v1/documents/messages", documentRequest(false, "What is on page two?", samplePDF()))
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var acc struct {
		Type      string      `json:"type"`
		Documents []docStatus `json:"documents"`
		Session   struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"session"`
	}
	json.NewDecoder(resp.Body).Decode(&acc)
	resp.Body.Close()
	if acc.Type != "documents_accepted" || len(acc.Documents) != 1 || acc.Session.Title != "What is on page two?" {
		t.Fatalf("accepted = %+v", acc)
	}
	e.waitDocument(t, acc.Documents[0].ID)

	resp = e.do(t, http.MethodPost, "/v1/messages", messageRequest(false, "What is on page two?", map[string]any{"session_id": acc.Session.ID}))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("question on the session: status %d", resp.StatusCode)
	}
}

func TestDocumentMessagesStreamingWaitsThenAnswers(t *testing.T) {
	e := setup(t)
	resp := e.do(t, http.MethodPost, "/v1/documents/messages", documentRequest(true, "Summarise it", samplePDF()))
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d (%s): %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	names := sseEvents(t, resp)
	i := 0
	for i < len(names) && names[i] == "document_progress" {
		i++
	}
	if i == 0 || i >= len(names) || names[i] != "message_start" || names[len(names)-1] != "message_stop" {
		t.Fatalf("events = %v", names)
	}
}

func TestDocumentMessagesRejectsBadInput(t *testing.T) {
	e := setup(t)
	for name, body := range map[string]map[string]any{
		"no question":  documentRequest(false, "", samplePDF()),
		"not a file":   documentRequest(false, "hi", []byte("plain text, not a document")),
		"no documents": {"model": "bepilot-fake-1", "max_tokens": 64, "messages": []map[string]any{{"role": "user", "content": "hi"}}},
	} {
		resp := e.do(t, http.MethodPost, "/v1/documents/messages", body)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
		}
	}
}
