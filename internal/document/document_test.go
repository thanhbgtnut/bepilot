package document

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"

	"github.com/thanhenti/bepilot/internal/config"
)

// fakeOCR answers /ocr/raw with docpipe's sample TurboOCR response; status
// non-zero makes every call fail with it.
func fakeOCR(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	sample, err := os.ReadFile("../../docpipe/parser/turboocr/testdata/output_example.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		calls.Add(1)
		if status != 0 {
			http.Error(w, "down", status)
			return
		}
		w.Write(sample)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newService(t *testing.T, ocrURL string) *Service {
	t.Helper()
	svc, err := New(config.Documents{
		RenderMode: "webassembly", RenderWorkers: 1, DPI: 50, Concurrency: 2,
		OCR: config.DocumentOCR{BaseURL: ocrURL, Timeout: 10 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc
}

func pngImage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParsePDFAndImage(t *testing.T) {
	ocr, calls := fakeOCR(t, 0)
	svc := newService(t, ocr.URL)
	ctx := context.Background()

	res, err := svc.Parse(ctx, File{Name: "a.pdf", Data: pdftest.Build([]pdftest.Page{{"PAGE ONE"}, {"PAGE TWO"}}, pdftest.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	if res.MediaType != TypePDF || len(res.Pages) != 2 || calls.Load() != 2 {
		t.Fatalf("pdf: media %q, %d pages, %d OCR calls", res.MediaType, len(res.Pages), calls.Load())
	}
	for _, p := range res.Pages {
		if p.Error != "" || strings.TrimSpace(p.Markdown) == "" || strings.TrimSpace(p.Text) == "" {
			t.Fatalf("page %d: error %q, markdown %d chars, text %d chars", p.PageNo, p.Error, len(p.Markdown), len(p.Text))
		}
	}
	if !strings.Contains(res.Markdown(), "\n\n---\n\n") {
		t.Fatal("joined markdown has no page separator")
	}

	img, err := svc.Parse(ctx, File{Name: "a.png", Data: pngImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	if img.MediaType != TypePNG || len(img.Pages) != 1 || img.Pages[0].Markdown == "" {
		t.Fatalf("image: %+v", img)
	}
}

// A page the OCR engine fails on keeps its error; the file still parses.
func TestParsePageErrorIsPerPage(t *testing.T) {
	ocr, _ := fakeOCR(t, http.StatusServiceUnavailable)
	svc := newService(t, ocr.URL)
	res, err := svc.Parse(context.Background(), File{Data: pngImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() != 1 || res.Pages[0].Error == "" {
		t.Fatalf("want one failed page, got %+v", res.Pages)
	}
}

func TestParseRejectsUnsupported(t *testing.T) {
	svc := newService(t, "http://127.0.0.1:1")
	if _, err := svc.Parse(context.Background(), File{Data: []byte("hello, not a document")}); err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
