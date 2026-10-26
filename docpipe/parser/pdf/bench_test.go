package pdf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser/pdf/pdftest"
)

// BenchmarkRenderA4 measures render + JPEG encode + text extraction of one
// A4-sized page at 300 DPI (N7a). Mode follows BEPAYLOT_RENDER_MODE
// (webassembly by default; multi_threaded needs the pdfium-worker binary in
// BEPAYLOT_PDFIUM_WORKER).
func BenchmarkRenderA4(b *testing.B) {
	mode := os.Getenv("BEPAYLOT_RENDER_MODE")
	if mode == "" {
		mode = "webassembly"
	}
	r, err := New(Config{Mode: mode, WorkerBin: os.Getenv("BEPAYLOT_PDFIUM_WORKER"), Workers: 1, PageTimeout: time.Minute})
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	lines := make(pdftest.Page, 40)
	for i := range lines {
		lines[i] = "Bao cao tai chinh nam 2023 - Tong tai san 655.103.771.111 dong - dong so " + string(rune('A'+i%26))
	}
	path := filepath.Join(b.TempDir(), "a4.pdf")
	if err := os.WriteFile(path, pdftest.Build([]pdftest.Page{lines}, pdftest.Options{}), 0o600); err != nil {
		b.Fatal(err)
	}
	opt := RenderOptions{DPI: 300, MaxLongSide: 4000, MaxPixels: 16_000_000, JPEGQuality: 85, ExtractText: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.RenderBatch(context.Background(), path, []int{1}, opt, func(PageRender) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}
