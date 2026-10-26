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

func TestDocumentMessagesBuffered(t *testing.T) {
	e := setup(t)
	resp := e.do(t, http.MethodPost, "/v1/documents/messages", documentRequest(false, "What is on page two?", samplePDF()))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		RequestID string `json:"request_id"`
		Documents []struct {
			Pages int `json:"pages"`
		} `json:"documents"`
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
		Session struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Type != "message" || out.Role != "assistant" || len(out.Content) == 0 {
		t.Fatalf("bad envelope: %+v", out)
	}
	if len(out.Documents) != 1 || out.Documents[0].Pages != 2 {
		t.Fatalf("documents = %+v", out.Documents)
	}
	if out.Session.Title != "What is on page two?" {
		t.Fatalf("session title = %q, want it from the question", out.Session.Title)
	}

	row := e.documentRow(t, out.RequestID)
	if row.Kind != "message" || row.Status != "ok" || row.Files != 1 || row.SessionID == nil || row.UserMsgID == nil || row.AsstMsgID == nil {
		t.Fatalf("stored row = %+v", row)
	}
	if row.SessionID.String() != out.Session.ID {
		t.Fatal("stored row points at another session")
	}
	if bytes.Contains(row.Request, []byte(`"data"`)) || !bytes.Contains(row.Request, []byte(`"file_index"`)) {
		t.Fatalf("stored request must reference the file, not carry it: %s", row.Request)
	}

	// The agent got the documents and the question as the user message.
	var text string
	if err := e.store.Pool.QueryRow(context.Background(),
		`SELECT text FROM content_blocks WHERE message_id = $1 AND type = 'text'`, *row.UserMsgID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<documents>", `<page number="2">`, "<markdown>", "<ocr_text>", "What is on page two?"} {
		if !strings.Contains(text, want) {
			t.Errorf("user message lacks %q", want)
		}
	}
}

func TestDocumentMessagesStreaming(t *testing.T) {
	e := setup(t)
	resp := e.do(t, http.MethodPost, "/v1/documents/messages", documentRequest(true, "Summarise it", samplePDF()))
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d (%s): %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	var names []string
	var requestID string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if ev, ok := strings.CutPrefix(line, "event:"); ok && strings.TrimSpace(ev) != "ping" {
			names = append(names, strings.TrimSpace(ev))
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok && strings.Contains(data, `"document_parsed"`) {
			var p struct {
				RequestID string `json:"request_id"`
			}
			json.Unmarshal([]byte(data), &p)
			requestID = p.RequestID
		}
	}
	if len(names) < 3 || names[0] != "document_parsed" || names[1] != "message_start" || names[len(names)-1] != "message_stop" {
		t.Fatalf("events = %v", names)
	}

	row := e.documentRow(t, requestID)
	var stored struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(row.Response, &stored)
	if row.Status != "ok" || row.AsstMsgID == nil || stored.Type != "message" || stored.RequestID != requestID {
		t.Fatalf("stored status %q, assistant message %v, response %s", row.Status, row.AsstMsgID, row.Response)
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
