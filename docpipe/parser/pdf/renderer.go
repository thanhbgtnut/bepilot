// Package pdf renders PDF pages to JPEG and extracts their text layer with
// go-pdfium (§5.7). PDFium runs in child processes (multi_threaded) in
// production, or in WebAssembly (wazero, no cgo) for development and CI.
//
// Resource control:
//   - CPU: one PDFium instance per slot; Workers slots in total.
//   - RAM: bitmaps live in the PDFium instance and are encoded to JPEG there;
//     MaxPixels caps every bitmap; instances are recycled after
//     RecycleAfterPages pages (and on Linux when a worker's RSS exceeds
//     MaxWorkerRSSMB).
//   - Hangs: a page exceeding PageTimeout kills its instance.
package pdf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	"github.com/klippa-app/go-pdfium/multi_threaded"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
	"github.com/klippa-app/go-pdfium/webassembly"

	"github.com/thanhenti/bepaylot/docpipe/parser/textlayer"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Config configures the renderer.
type Config struct {
	Mode              string // multi_threaded | webassembly
	WorkerBin         string
	Workers           int
	RecycleAfterPages int
	MaxWorkerRSSMB    int
	PageTimeout       time.Duration
	Log               *slog.Logger
}

// RenderOptions tunes one batch.
type RenderOptions struct {
	DPI         int
	MaxLongSide int
	MaxPixels   int
	JPEGQuality int
	ExtractText bool
}

// PageSize is a page's size in PDF points.
type PageSize struct {
	WidthPt, HeightPt float64
}

// DocInfo is the result of Probe.
type DocInfo struct {
	PageCount int
	Pages     []PageSize
	Info      map[string]string
	Bookmarks []types.PDFBookmark
}

// PageRender is one rendered page.
type PageRender struct {
	PageNo            int
	JPEG              []byte
	Width, Height     int
	DPI               float64
	WidthPt, HeightPt float64
	Rotation          int
	Text              *types.TextLayer
	RenderMs, TextMs  int
}

// ErrPageTimeout marks a page that exceeded PageTimeout.
var ErrPageTimeout = errors.New("pdf: page render timed out")

type slot struct {
	inst  pdfium.Pdfium
	pages int
}

// Renderer owns a fixed set of PDFium instances.
type Renderer struct {
	cfg   Config
	pool  pdfium.Pool
	slots chan *slot
	log   *slog.Logger

	mu     sync.Mutex
	closed bool
}

// New starts Workers PDFium instances.
func New(cfg Config) (*Renderer, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.PageTimeout <= 0 {
		cfg.PageTimeout = 30 * time.Second
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	var pool pdfium.Pool
	switch cfg.Mode {
	case "multi_threaded":
		bin, err := findWorkerBin(cfg.WorkerBin)
		if err != nil {
			return nil, err
		}
		cfg.WorkerBin = bin
		pool = multi_threaded.Init(multi_threaded.Config{
			MinIdle: cfg.Workers, MaxIdle: cfg.Workers, MaxTotal: cfg.Workers,
			Command:     multi_threaded.Command{BinPath: cfg.WorkerBin, StartTimeout: 30 * time.Second},
			LogCallback: func(s string) { log.Debug("pdfium worker", "msg", s) },
		})
	case "webassembly", "":
		// Headroom: a timed-out WebAssembly call cannot be interrupted, so its
		// instance is retired in the background while a replacement starts.
		p, err := webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: cfg.Workers, MaxTotal: 2 * cfg.Workers})
		if err != nil {
			return nil, fmt.Errorf("pdf: init webassembly: %w", err)
		}
		pool = p
	default:
		return nil, fmt.Errorf("pdf: unknown render mode %q", cfg.Mode)
	}
	r := &Renderer{cfg: cfg, pool: pool, slots: make(chan *slot, cfg.Workers), log: log}
	for i := 0; i < cfg.Workers; i++ {
		inst, err := pool.GetInstance(60 * time.Second)
		if err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("pdf: start instance %d: %w", i, err)
		}
		r.slots <- &slot{inst: inst}
	}
	return r, nil
}

// Close stops every instance.
func (r *Renderer) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	for {
		select {
		case s := <-r.slots:
			_ = s.inst.Close()
		default:
			return r.pool.Close()
		}
	}
}

func (r *Renderer) acquire(ctx context.Context) (*slot, error) {
	select {
	case s := <-r.slots:
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// release returns the slot, replacing its instance when it is due for
// recycling or was killed.
func (r *Renderer) release(s *slot, kill bool) {
	due := r.cfg.RecycleAfterPages > 0 && s.pages >= r.cfg.RecycleAfterPages
	if !due && r.cfg.MaxWorkerRSSMB > 0 && maxWorkerRSSMB(r.cfg.WorkerBin) > r.cfg.MaxWorkerRSSMB {
		due = true
	}
	if kill || due {
		r.retire(s.inst, kill)
		inst, err := r.pool.GetInstance(60 * time.Second)
		if err != nil {
			r.log.Error("pdf: respawn instance failed", "err", err)
			// Keep capacity: retry lazily on next acquire.
			go func() {
				for {
					time.Sleep(time.Second)
					if inst, err := r.pool.GetInstance(60 * time.Second); err == nil {
						r.slots <- &slot{inst: inst}
						return
					}
				}
			}()
			return
		}
		r.log.Debug("pdf: instance recycled", "pages", s.pages, "killed", kill)
		s = &slot{inst: inst}
	}
	r.slots <- s
}

// retire discards an instance. Child processes are killed; WebAssembly
// instances are closed (go-pdfium's WebAssembly Kill leaks the pool slot), in
// the background when a timed-out call may still hold the instance lock.
func (r *Renderer) retire(inst pdfium.Pdfium, stuck bool) {
	if r.cfg.Mode == "multi_threaded" {
		_ = inst.Kill()
		return
	}
	if stuck {
		go func() { _ = inst.Close() }()
		return
	}
	_ = inst.Close()
}

// call runs fn on the slot with the page timeout; a timeout kills the slot.
func call[T any](r *Renderer, s *slot, fn func(pdfium.Pdfium) (T, error)) (T, bool, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := fn(s.inst)
		ch <- res{v, err}
	}()
	t := time.NewTimer(r.cfg.PageTimeout)
	defer t.Stop()
	select {
	case out := <-ch:
		return out.v, false, out.err
	case <-t.C:
		if r.cfg.Mode == "multi_threaded" {
			_ = s.inst.Kill() // unblocks the RPC call
		}
		var zero T
		return zero, true, ErrPageTimeout
	}
}

// Probe opens path and reads page count, sizes, Info metadata and bookmarks.
func (r *Renderer) Probe(ctx context.Context, path string) (*DocInfo, error) {
	s, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	info, killed, err := call(r, s, func(p pdfium.Pdfium) (*DocInfo, error) { return r.probe(p, path) })
	r.release(s, killed)
	return info, err
}

func (r *Renderer) probe(p pdfium.Pdfium, path string) (*DocInfo, error) {
	doc, done, err := r.openDocument(p, path)
	if err != nil {
		return nil, fmt.Errorf("pdf: open: %w", err)
	}
	defer done()
	defer p.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})

	cnt, err := p.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("pdf: page count: %w", err)
	}
	info := &DocInfo{PageCount: cnt.PageCount, Info: map[string]string{}}
	for i := 0; i < cnt.PageCount; i++ {
		sz, err := p.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{Document: doc.Document, Index: i})
		if err != nil {
			return nil, fmt.Errorf("pdf: page %d size: %w", i+1, err)
		}
		info.Pages = append(info.Pages, PageSize{WidthPt: sz.Width, HeightPt: sz.Height})
	}
	for _, tag := range []string{"Title", "Author", "Subject", "Keywords", "Creator", "Producer", "CreationDate", "ModDate"} {
		if m, err := p.FPDF_GetMetaText(&requests.FPDF_GetMetaText{Document: doc.Document, Tag: tag}); err == nil && strings.TrimSpace(m.Value) != "" {
			info.Info[tag] = strings.TrimSpace(m.Value)
		}
	}
	if bm, err := p.GetBookmarks(&requests.GetBookmarks{Document: doc.Document}); err == nil {
		info.Bookmarks = convertBookmarks(bm.Bookmarks)
	}
	return info, nil
}

func convertBookmarks(in []responses.GetBookmarksBookmark) []types.PDFBookmark {
	out := make([]types.PDFBookmark, 0, len(in))
	for _, b := range in {
		page := 0
		switch {
		case b.DestInfo != nil:
			page = b.DestInfo.PageIndex + 1
		case b.ActionInfo != nil && b.ActionInfo.DestInfo != nil:
			page = b.ActionInfo.DestInfo.PageIndex + 1
		}
		out = append(out, types.PDFBookmark{Title: strings.TrimSpace(b.Title), Page: page, Children: convertBookmarks(b.Children)})
	}
	return out
}

// EffectiveDPI returns the DPI that keeps a page within the long-side and
// pixel caps, never above the requested DPI (§5.7).
func EffectiveDPI(sz PageSize, opt RenderOptions) int {
	dpi := float64(opt.DPI)
	wIn, hIn := sz.WidthPt/72, sz.HeightPt/72
	if long := max(wIn, hIn); opt.MaxLongSide > 0 && long > 0 {
		dpi = min(dpi, float64(opt.MaxLongSide)/long)
	}
	if area := wIn * hIn; opt.MaxPixels > 0 && area > 0 {
		dpi = min(dpi, math.Sqrt(float64(opt.MaxPixels)/area))
	}
	return max(int(math.Floor(dpi)), 36)
}

// RenderBatch opens path once and renders the given 1-based pages in order,
// calling emit as each page finishes. A page error stops the batch; pages
// already emitted stay done.
func (r *Renderer) RenderBatch(ctx context.Context, path string, pages []int, opt RenderOptions, emit func(PageRender) error) error {
	s, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	killed := false
	defer func() { r.release(s, killed) }()

	done := func() {}
	doc, k, err := call(r, s, func(p pdfium.Pdfium) (*responses.OpenDocument, error) {
		d, rel, err := r.openDocument(p, path)
		done = rel
		return d, err
	})
	if err != nil {
		killed = k
		return fmt.Errorf("pdf: open: %w", err)
	}
	defer func() {
		if !killed {
			_, _ = s.inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
		}
		done()
	}()

	for _, pageNo := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		pr, k, err := call(r, s, func(p pdfium.Pdfium) (PageRender, error) { return renderPage(p, doc.Document, pageNo, opt) })
		s.pages++
		if err != nil {
			killed = k
			return fmt.Errorf("pdf: page %d: %w", pageNo, err)
		}
		if err := emit(pr); err != nil {
			return err
		}
	}
	return nil
}

func renderPage(p pdfium.Pdfium, doc references.FPDF_DOCUMENT, pageNo int, opt RenderOptions) (PageRender, error) {
	idx := pageNo - 1
	sz, err := p.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{Document: doc, Index: idx})
	if err != nil {
		return PageRender{}, err
	}
	dpi := EffectiveDPI(PageSize{WidthPt: sz.Width, HeightPt: sz.Height}, opt)
	page := requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: idx}}

	t0 := time.Now()
	img, err := p.RenderToFile(&requests.RenderToFile{
		RenderPageInDPI: &requests.RenderPageInDPI{Page: page, DPI: dpi},
		OutputFormat:    requests.RenderToFileOutputFormatJPG,
		OutputTarget:    requests.RenderToFileOutputTargetBytes,
		OutputQuality:   opt.JPEGQuality,
	})
	if err != nil {
		return PageRender{}, fmt.Errorf("render: %w", err)
	}
	if img.ImageBytes == nil {
		return PageRender{}, errors.New("render: empty image")
	}
	out := PageRender{
		PageNo: pageNo, JPEG: *img.ImageBytes, DPI: float64(dpi),
		WidthPt: sz.Width, HeightPt: sz.Height, RenderMs: int(time.Since(t0).Milliseconds()),
	}
	if len(img.Pages) > 0 {
		out.Width, out.Height = img.Pages[0].Width, img.Pages[0].Height
	} else {
		out.Width, out.Height = int(sz.Width*float64(dpi)/72), int(sz.Height*float64(dpi)/72)
	}
	if rot, err := p.FPDFPage_GetRotation(&requests.FPDFPage_GetRotation{Page: page}); err == nil {
		out.Rotation = rotationDegrees(rot.PageRotation)
	}

	if opt.ExtractText {
		t1 := time.Now()
		// go-pdfium's own "pixel positions" only scale PDF points (origin
		// bottom-left, no rotation), so map points with PDFium's page→device
		// transform of the rendered image instead.
		txt, err := p.GetPageTextStructured(&requests.GetPageTextStructured{Page: page, Mode: requests.GetPageTextStructuredModeChars})
		if err == nil {
			if tr, terr := deviceTransform(p, page, out.Width, out.Height); terr == nil {
				out.Text = &types.TextLayer{Words: textlayer.WordsFromChars(charsOf(txt, tr))}
			}
		}
		out.TextMs = int(time.Since(t1).Milliseconds())
	}
	return out, nil
}

// affine maps page points to image pixels: px = a*x + b*y + c, py = d*x + e*y + f.
type affine struct{ a, b, c, d, e, f float64 }

func (t affine) apply(x, y float64) (float64, float64) {
	return t.a*x + t.b*y + t.c, t.d*x + t.e*y + t.f
}

// deviceTransform derives the page→image affine transform from three
// FPDF_PageToDevice probes on a 16x oversampled device (to limit integer
// rounding). It honours the page's /Rotate and crop box like rendering does.
func deviceTransform(p pdfium.Pdfium, page requests.Page, w, h int) (affine, error) {
	const scale, step = 16.0, 1000.0
	probe := func(x, y float64) (float64, float64, error) {
		r, err := p.FPDF_PageToDevice(&requests.FPDF_PageToDevice{
			Page: page, SizeX: w * int(scale), SizeY: h * int(scale), Rotate: enums.FPDF_PAGE_ROTATION_NONE, PageX: x, PageY: y,
		})
		if err != nil {
			return 0, 0, err
		}
		return float64(r.DeviceX) / scale, float64(r.DeviceY) / scale, nil
	}
	x0, y0, err := probe(0, 0)
	if err != nil {
		return affine{}, err
	}
	x1, y1, err := probe(step, 0)
	if err != nil {
		return affine{}, err
	}
	x2, y2, err := probe(0, step)
	if err != nil {
		return affine{}, err
	}
	return affine{a: (x1 - x0) / step, b: (x2 - x0) / step, c: x0, d: (y1 - y0) / step, e: (y2 - y0) / step, f: y0}, nil
}

func charsOf(t *responses.GetPageTextStructured, tr affine) []textlayer.Char {
	out := make([]textlayer.Char, 0, len(t.Chars))
	for _, c := range t.Chars {
		pp := c.PointPosition
		ax, ay := tr.apply(pp.Left, pp.Bottom)
		bx, by := tr.apply(pp.Right, pp.Top)
		out = append(out, textlayer.Char{Text: c.Text, BBox: types.BBox{
			X0: min(ax, bx), Y0: min(ay, by), X1: max(ax, bx), Y1: max(ay, by),
		}})
	}
	return out
}

func rotationDegrees(r enums.FPDF_PAGE_ROTATION) int {
	switch r {
	case enums.FPDF_PAGE_ROTATION_90_CW:
		return 90
	case enums.FPDF_PAGE_ROTATION_180_CW:
		return 180
	case enums.FPDF_PAGE_ROTATION_270_CW:
		return 270
	}
	return 0
}
