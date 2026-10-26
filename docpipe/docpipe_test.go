package docpipe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf"
	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/docpipe/parser/turboocr"
	"github.com/thanhenti/bepaylot/docpipe/parser/vlm"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// scripted answers ClassifyPage by image, in the order of the PDF pages.
type scripted struct {
	mu      sync.Mutex
	answers map[string]types.PageDocType
	labels  []string
	fail    string // image that fails
	texts   []string
}

func (s *scripted) ClassifyPage(_ context.Context, labels []types.ClassLabel, image []byte, text string) (string, float64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
	s.labels = s.labels[:0]
	for _, l := range labels {
		s.labels = append(s.labels, l.Name)
	}
	if string(image) == s.fail {
		return "", 0, false, errors.New("decision api down")
	}
	a := s.answers[string(image)]
	return a.Label, a.Confidence, a.FirstPage, nil
}

// The whole flow on a four-page PDF: two contracts scanned one after the
// other, then an invoice. Pages are rendered, read by TurboOCR (/ocr/raw)
// and by the VLM through the OpenAI-compatible client, then classified with
// their OCR text and grouped at the first pages.
func TestRun(t *testing.T) {
	sample, err := os.ReadFile("parser/turboocr/testdata/output_example.json")
	if err != nil {
		t.Fatal(err)
	}
	var ocrQuery string
	var ocrCalls int
	var mu sync.Mutex
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		ocrQuery = r.URL.RawQuery
		ocrCalls++
		mu.Unlock()
		w.Write(sample)
	}))
	defer ocr.Close()
	var vlmCalls int
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content []struct {
					Type, Text string
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("vlm request: %s %v", r.URL.Path, err)
		}
		if p := req.Messages[0].Content[0].Text; strings.Contains(p, "UBND XÃ NHA BÍCH") {
			t.Errorf("the prompt must not carry the OCR text of the page")
		}
		mu.Lock()
		vlmCalls++
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "# GIẤY CHỨNG NHẬN\n\nUBND XÃ NHA BÍCH"}, "finish_reason": "stop"}}})
	}))
	defer llm.Close()

	path := filepath.Join(t.TempDir(), "a.pdf")
	if err := os.WriteFile(path, pdftest.Build([]pdftest.Page{{"HOP DONG 01"}, {"Dieu 2"}, {"HOP DONG 02"}, {"HOA DON"}}, pdftest.Options{}), 0o600); err != nil {
		t.Fatal(err)
	}
	renderer, err := pdf.New(pdf.Config{Mode: "webassembly", Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()

	layout := turboocr.New(turboocr.Config{BaseURL: ocr.URL, Timeout: 10 * time.Second})
	engine, err := vlm.New(vlm.Config{Layout: layout, Client: vlm.NewOpenAI(vlm.OpenAIConfig{BaseURL: llm.URL + "/v1", Model: "m"})})
	if err != nil {
		t.Fatal(err)
	}
	clf := &scripted{answers: map[string]types.PageDocType{}}
	opt := pdf.RenderOptions{DPI: 50, JPEGQuality: 60}
	p, err := New(Config{Renderer: renderer, Render: opt, Classifier: clf, Labels: []types.ClassLabel{{Name: "hop_dong", Title: "Hợp đồng"}, {Name: "hoa_don", Title: "Hoá đơn"}}, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	// The classifier is scripted by page image: render the pages once to know them.
	var images []string
	if err := renderer.RenderBatch(context.Background(), path, []int{1, 2, 3, 4}, opt, func(r pdf.PageRender) error {
		images = append(images, string(r.JPEG))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clf.answers[images[0]] = types.PageDocType{Label: "hop_dong", Confidence: 0.9, FirstPage: true}
	clf.fail = images[1] // a failed page continues the document before it
	clf.answers[images[2]] = types.PageDocType{Label: "hop_dong", Confidence: 0.9, FirstPage: true}
	clf.answers[images[3]] = types.PageDocType{Label: "hoa_don", Confidence: 0.95, FirstPage: true}

	res, err := p.Run(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Document{{1, 2, "hop_dong", "", 0.9}, {3, 3, "hop_dong", "", 0.9}, {4, 4, "hoa_don", "", 0.95}}
	if len(res.Documents) != len(want) {
		t.Fatalf("documents = %+v", res.Documents)
	}
	for i, d := range res.Documents {
		if d != want[i] {
			t.Errorf("document %d = %+v, want %+v", i, d, want[i])
		}
	}
	// Every page is classified after OCR, with the OCR text of the page.
	if len(clf.texts) != 4 {
		t.Fatalf("classify calls = %d", len(clf.texts))
	}
	for i, text := range clf.texts {
		if !strings.Contains(text, "UBND XÃ NHA BÍCH") {
			t.Errorf("classify call %d: no OCR text (%q)", i+1, text)
		}
	}
	if got := strings.Join(clf.labels, ","); got != "hop_dong,hoa_don,other" {
		t.Errorf("choices = %s", got)
	}
	if len(res.Pages) != 4 || res.Pages[1].ClassifyErr == nil || res.Pages[0].ClassifyErr != nil {
		t.Fatalf("pages = %d, classify errors: %v %v", len(res.Pages), res.Pages[0].ClassifyErr, res.Pages[1].ClassifyErr)
	}
	for i, pg := range res.Pages {
		if pg.PageNo != i+1 || len(pg.JPEG) == 0 || pg.OCRErr != nil || pg.Parsed == nil || len(pg.Raw) == 0 {
			t.Fatalf("page %d: %+v", i+1, pg.OCRErr)
		}
		if pg.Parsed.Engine != vlm.Name || len(pg.Parsed.Lines) == 0 || !strings.Contains(pg.Parsed.Markdown, "UBND XÃ NHA BÍCH") {
			t.Errorf("page %d: engine %q, %d lines, markdown %q", i+1, pg.Parsed.Engine, len(pg.Parsed.Lines), pg.Parsed.Markdown)
		}
	}
	if ocrCalls != 4 || vlmCalls != 4 || ocrQuery != "autorotate=1&layout=1&reading_order=1&tables=1" {
		t.Errorf("ocr calls %d (%s), vlm calls %d", ocrCalls, ocrQuery, vlmCalls)
	}

	// Without a classifier the pages are read but not grouped; an OCR
	// failure stays on its page.
	ocr.Close()
	p, _ = New(Config{Renderer: renderer, Render: opt, Engine: layout})
	res, err = p.Run(context.Background(), path)
	if err != nil || len(res.Documents) != 0 || len(res.Pages) != 4 || res.Pages[0].OCRErr == nil || res.Pages[0].Parsed != nil {
		t.Fatalf("no classifier, OCR down: %v %+v", err, res)
	}
	if _, err := New(Config{Renderer: renderer}); err == nil {
		t.Error("an engine is required")
	}
}
