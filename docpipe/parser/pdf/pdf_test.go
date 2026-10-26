package pdf

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/docpipe/parser/textlayer"
)

var (
	sharedOnce sync.Once
	shared     *Renderer
	sharedErr  error
)

// renderer returns one WebAssembly renderer for the package's tests (starting
// wazero is the slow part).
func renderer(t *testing.T) *Renderer {
	t.Helper()
	if testing.Short() {
		t.Skip("pdfium webassembly tests skipped in -short mode")
	}
	sharedOnce.Do(func() {
		shared, sharedErr = New(Config{Mode: "webassembly", Workers: 1, PageTimeout: 60 * time.Second, RecycleAfterPages: 3})
	})
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return shared
}

func writePDF(t *testing.T, pages []pdftest.Page, opt pdftest.Options) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(p, pdftest.Build(pages, opt), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbe(t *testing.T) {
	r := renderer(t)
	path := writePDF(t, []pdftest.Page{{"Chapter One"}, {"Chapter Two"}}, pdftest.Options{Outline: true, Title: "Test Report"})
	info, err := r.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info.PageCount != 2 || info.Pages[0].WidthPt != 612 || info.Pages[0].HeightPt != 792 {
		t.Fatalf("info = %+v", info)
	}
	if info.Info["Title"] != "Test Report" {
		t.Fatalf("info dict = %v", info.Info)
	}
	if len(info.Bookmarks) != 2 || info.Bookmarks[1].Title != "Chapter Two" || info.Bookmarks[1].Page != 2 {
		t.Fatalf("bookmarks = %+v", info.Bookmarks)
	}
}

func TestRenderBatchImageAndTextLayer(t *testing.T) {
	r := renderer(t)
	path := writePDF(t, []pdftest.Page{
		{"Invoice number 0101021398", "Total amount 50.000.000"},
		{"Second page text"},
	}, pdftest.Options{})
	var got []PageRender
	err := r.RenderBatch(context.Background(), path, []int{1, 2}, RenderOptions{DPI: 100, MaxLongSide: 4000, MaxPixels: 16_000_000, JPEGQuality: 80, ExtractText: true},
		func(p PageRender) error { got = append(got, p); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("pages = %d", len(got))
	}
	p := got[0]
	if p.Width != 850 || p.Height != 1100 || p.DPI != 100 {
		t.Fatalf("size %dx%d dpi %v", p.Width, p.Height, p.DPI)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(p.JPEG))
	if err != nil || cfg.Width != p.Width {
		t.Fatalf("jpeg decode: %v %+v", err, cfg)
	}
	text := textlayer.Text(p.Text)
	if !strings.Contains(text, "0101021398") || !strings.Contains(text, "50.000.000") {
		t.Fatalf("text layer = %q", text)
	}
	// Pixel positions must fall inside the rendered image, near the top-left
	// where the text was placed (72pt from the left edge = 100px at 100 DPI).
	w := p.Text.Words[0]
	if w.BBox.X0 < 90 || w.BBox.X0 > 110 || w.BBox.Y1 > 200 {
		t.Fatalf("first word box = %+v", w.BBox)
	}
}

func TestEffectiveDPICapsHugePages(t *testing.T) {
	a4 := PageSize{WidthPt: 595, HeightPt: 842}
	opt := RenderOptions{DPI: 300, MaxLongSide: 4000, MaxPixels: 16_000_000}
	if d := EffectiveDPI(a4, opt); d != 300 {
		t.Fatalf("A4 dpi = %d", d)
	}
	a0 := PageSize{WidthPt: 2384, HeightPt: 3370}
	d := EffectiveDPI(a0, opt)
	w, h := 2384.0/72*float64(d), 3370.0/72*float64(d)
	if w*h > 16_000_000 || max(w, h) > 4000 {
		t.Fatalf("A0 at %d dpi = %.0fx%.0f exceeds caps", d, w, h)
	}
}

func TestPDFADetector(t *testing.T) {
	doc := pdftest.Build([]pdftest.Page{{"x"}}, pdftest.Options{PDFAPart: 2, PDFAConformance: "u"})
	var d PDFADetector
	// Feed in small chunks so the marker straddles writes.
	for i := 0; i < len(doc); i += 7 {
		d.Write(doc[i:min(i+7, len(doc))])
	}
	if d.Part != 2 || d.Conform != "u" || !d.Trusted() {
		t.Fatalf("detector = %+v", d)
	}
	var plain PDFADetector
	plain.Write(pdftest.Build([]pdftest.Page{{"x"}}, pdftest.Options{}))
	if plain.IsPDFA() {
		t.Fatal("plain PDF detected as PDF/A")
	}
}

func TestRecycleKeepsWorking(t *testing.T) {
	r := renderer(t)
	path := writePDF(t, []pdftest.Page{{"a"}, {"b"}, {"c"}, {"d"}}, pdftest.Options{})
	for i := 0; i < 2; i++ { // 8 pages > RecycleAfterPages=3
		n := 0
		err := r.RenderBatch(context.Background(), path, []int{1, 2, 3, 4}, RenderOptions{DPI: 50, JPEGQuality: 60},
			func(PageRender) error { n++; return nil })
		if err != nil || n != 4 {
			t.Fatalf("round %d: n=%d err=%v", i, n, err)
		}
	}
}
