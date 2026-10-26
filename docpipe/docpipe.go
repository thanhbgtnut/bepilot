// Package docpipe is the document flow of bepaylot as a library: a PDF is
// rendered to page images, every page is read by the OCR engine (TurboOCR
// /ocr/raw, or TurboOCR followed by one VLM call per page) into lines,
// blocks and markdown, every page is classified from its image and its OCR
// text (document type and is_first_page), and the pages are grouped into
// documents at the first pages.
//
// It keeps nothing: no database, no object storage, no queue. Run returns
// the page images and everything read from them.
package docpipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/assemble"
	"github.com/thanhenti/bepaylot/docpipe/parser/pdf"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Classifier reads the document type of one page from its image and its OCR
// text, and whether the page is the first page of a document.
// decision.Client is one.
type Classifier interface {
	ClassifyPage(ctx context.Context, labels []types.ClassLabel, image []byte, text string) (label string, confidence float64, isFirstPage bool, err error)
}

// Config configures a Pipeline.
type Config struct {
	// Renderer turns PDF pages into JPEG images.
	Renderer *pdf.Renderer
	// Render tunes the page images; the zero value is 300 DPI, long side at
	// most 4000 px, at most 16 MP, JPEG quality 85.
	Render pdf.RenderOptions

	// Classifier and Labels (the document types to choose from) classify the
	// pages; without a Classifier pages are not classified and Result has no
	// Documents. types.LabelOther is always added to the choices.
	Classifier Classifier
	Labels     []types.ClassLabel
	// MinConfidence is the confidence a first page needs for its document to
	// take its type (default 0.6).
	MinConfidence float64

	// Engine reads a page image: turboocr.Client, or vlm.Engine on top of it.
	Engine parser.Engine
	// Options are the engine features; nil turns on autorotate, layout,
	// reading order, tables and the VLM step.
	Options *parser.PageOptions

	// Concurrency is the number of pages read, then classified, at once
	// (default 4).
	Concurrency int
}

// Page is one page of the PDF and what was read from it.
type Page struct {
	PageNo        int
	JPEG          []byte
	Width, Height int
	// Type is the classifier's answer; Label is "" when the page was not
	// classified, and ClassifyErr says why when the call failed.
	Type        types.PageDocType
	ClassifyErr error
	// Parsed holds the lines, blocks and markdown of the page, and Raw the
	// engine's own response; both are nil when the engine failed (OCRErr).
	Parsed *types.ParsedPage
	Raw    []byte
	OCRErr error
}

// Result is what Run read from one PDF.
type Result struct {
	Pages     []Page
	Documents []Document
}

// Pipeline runs the flow. It is safe for concurrent use.
type Pipeline struct {
	cfg Config
}

// New checks the configuration and fills the defaults.
func New(cfg Config) (*Pipeline, error) {
	if cfg.Renderer == nil || cfg.Engine == nil {
		return nil, errors.New("docpipe: renderer and engine are required")
	}
	if cfg.Render == (pdf.RenderOptions{}) {
		cfg.Render = pdf.RenderOptions{DPI: 300, MaxLongSide: 4000, MaxPixels: 16_000_000, JPEGQuality: 85}
	}
	if cfg.MinConfidence <= 0 {
		cfg.MinConfidence = 0.6
	}
	if cfg.Options == nil {
		cfg.Options = &parser.PageOptions{Autorotate: true, Layout: true, ReadingOrder: true, Tables: true, Refine: true}
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	// "other" is always a choice: without it a page of no listed type (a
	// blank page, a letter) is forced into one of them, often confidently.
	cfg.Labels = append(append([]types.ClassLabel(nil), cfg.Labels...),
		types.ClassLabel{Name: types.LabelOther, Title: "Khác", Description: "trang trắng, hoặc giấy tờ không thuộc các loại trên"})
	return &Pipeline{cfg: cfg}, nil
}

// Run reads the PDF at path in three steps: render every page, read every
// page with the engine, then classify every page from its image and OCR
// text and group the pages into documents. Classification and OCR never
// run at the same time, so one
// server can hold both models. A page the classifier or the engine fails on
// keeps its error and does not fail the run; Run fails when the PDF cannot
// be rendered or ctx is done.
func (p *Pipeline) Run(ctx context.Context, path string) (*Result, error) {
	info, err := p.cfg.Renderer.Probe(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("docpipe: %w", err)
	}
	nos := make([]int, info.PageCount)
	for i := range nos {
		nos[i] = i + 1
	}
	res := &Result{Pages: make([]Page, 0, len(nos))}
	dpi := make([]float64, 0, len(nos))
	err = p.cfg.Renderer.RenderBatch(ctx, path, nos, p.cfg.Render, func(r pdf.PageRender) error {
		res.Pages = append(res.Pages, Page{PageNo: r.PageNo, JPEG: r.JPEG, Width: r.Width, Height: r.Height, Type: types.PageDocType{PageNo: r.PageNo}})
		dpi = append(dpi, r.DPI)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("docpipe: %w", err)
	}

	p.each(ctx, res.Pages, func(pg *Page, i int) {
		raw, err := p.cfg.Engine.ParsePage(ctx, parser.PageImage{PageNo: pg.PageNo, Body: bytes.NewReader(pg.JPEG), Size: int64(len(pg.JPEG)), Width: pg.Width, Height: pg.Height}, *p.cfg.Options)
		if err != nil {
			pg.OCRErr = err
			return
		}
		pg.Parsed = assemble.Build(raw, assemble.Options{PageNo: pg.PageNo, DPI: dpi[i], Engine: p.cfg.Engine.Name(), ReadingOrderFix: true, LowConfThreshold: 0.6})
		assemble.Render(pg.Parsed)
		pg.Raw = raw.Raw
	})

	if p.cfg.Classifier != nil {
		// The OCR text goes with the image: two types whose names differ by
		// one word are told apart by the words of the title. A page the
		// engine failed on is classified by its image alone.
		p.each(ctx, res.Pages, func(pg *Page, _ int) {
			text := ""
			if pg.Parsed != nil {
				text = assemble.PlainText(pg.Parsed)
			}
			t := &pg.Type
			t.Label, t.Confidence, t.FirstPage, pg.ClassifyErr = p.cfg.Classifier.ClassifyPage(ctx, p.cfg.Labels, pg.JPEG, text)
		})
		pts := make([]types.PageDocType, len(res.Pages))
		for i, pg := range res.Pages {
			pts[i] = pg.Type
		}
		res.Documents = GroupByFirstPage(pts, p.cfg.MinConfidence)
	}
	return res, ctx.Err()
}

// each calls fn for every page, Concurrency pages at a time.
func (p *Pipeline) each(ctx context.Context, pages []Page, fn func(pg *Page, i int)) {
	sem := make(chan struct{}, p.cfg.Concurrency)
	var wg sync.WaitGroup
	for i := range pages {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			fn(&pages[i], i)
		}()
	}
	wg.Wait()
}
