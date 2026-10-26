// Package document reads uploaded PDFs and images with docpipe: a PDF is
// rendered page by page, an image is one page, and every page is read by the
// OCR engine (TurboOCR, optionally followed by a VLM) into markdown and plain
// OCR text.
package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/thanhenti/bepaylot/docpipe"
	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/assemble"
	"github.com/thanhenti/bepaylot/docpipe/parser/imagefile"
	"github.com/thanhenti/bepaylot/docpipe/parser/pdf"
	"github.com/thanhenti/bepaylot/docpipe/parser/turboocr"
	"github.com/thanhenti/bepaylot/docpipe/parser/vlm"
	"github.com/thanhenti/bepaylot/docpipe/types"

	"github.com/thanhenti/bepilot/internal/config"
)

// Media types the service reads.
const (
	TypePDF  = "application/pdf"
	TypeJPEG = "image/jpeg"
	TypePNG  = "image/png"
	TypeTIFF = "image/tiff"
)

// ErrUnsupported is returned for a file that is not a PDF, JPEG, PNG or TIFF.
var ErrUnsupported = errors.New("unsupported file type (want PDF, JPEG, PNG or TIFF)")

// ErrUnreadable wraps the error for a file of a supported type that cannot be
// opened (a corrupt or encrypted PDF, a truncated image).
var ErrUnreadable = errors.New("file cannot be read")

// File is one uploaded document.
type File struct {
	Name      string
	MediaType string // as declared by the client; the content decides
	Data      []byte
}

// Page is what was read from one page. Error is set, and Markdown and Text
// are empty, when the engine failed on that page.
type Page struct {
	PageNo   int    `json:"page_no"`
	Markdown string `json:"markdown"`
	Text     string `json:"text"`
	Error    string `json:"error,omitempty"`
	// Numbers is the check of the markdown's numbers against the OCR text
	// (documents.vlm.number_check); nil when it did not run.
	Numbers *vlm.NumberReport `json:"numbers,omitempty"`
}

// Unverified is how many numbers of the page are still in doubt.
func (p Page) Unverified() int {
	if p.Numbers == nil {
		return 0
	}
	return len(p.Numbers.Unverified)
}

// Result is what was read from one file.
type Result struct {
	MediaType string `json:"media_type"` // detected from the content
	Pages     []Page `json:"pages"`
}

// Markdown joins the pages' markdown, separated by a rule.
func (r Result) Markdown() string {
	parts := make([]string, 0, len(r.Pages))
	for _, p := range r.Pages {
		if p.Markdown != "" {
			parts = append(parts, strings.TrimSpace(p.Markdown))
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// Failed reports how many pages the engine could not read.
func (r Result) Failed() int {
	n := 0
	for _, p := range r.Pages {
		if p.Error != "" {
			n++
		}
	}
	return n
}

// Service reads documents. It is safe for concurrent use.
type Service struct {
	renderer    *pdf.Renderer
	render      pdf.RenderOptions
	concurrency int
	pipe        *docpipe.Pipeline
	engine      parser.Engine
	opts        parser.PageOptions
	tmpDir      string
}

// New builds the renderer, the OCR engine (with the VLM step when
// cfg.VLM.BaseURL is set) and the pipeline. Close releases the renderer.
func New(cfg config.Documents) (*Service, error) {
	renderer, err := pdf.New(pdf.Config{Mode: cfg.RenderMode, Workers: cfg.RenderWorkers})
	if err != nil {
		return nil, fmt.Errorf("document: pdf renderer: %w", err)
	}
	var engine parser.Engine = turboocr.New(turboocr.Config{BaseURL: cfg.OCR.BaseURL, Timeout: cfg.OCR.Timeout})
	if cfg.VLM.BaseURL != "" {
		engine, err = vlm.New(vlm.Config{
			Layout: engine,
			Client: vlm.NewOpenAI(vlm.OpenAIConfig{BaseURL: cfg.VLM.BaseURL, APIKey: cfg.VLM.APIKey, Model: cfg.VLM.Model, Timeout: cfg.VLM.Timeout}),

			NumberCheck:   cfg.VLM.NumberCheck,
			ConfirmPrompt: cfg.VLM.ConfirmPrompt,
		})
		if err != nil {
			renderer.Close()
			return nil, fmt.Errorf("document: vlm engine: %w", err)
		}
	}
	render := pdf.RenderOptions{}
	if cfg.DPI > 0 {
		render = pdf.RenderOptions{DPI: cfg.DPI, MaxLongSide: 4000, MaxPixels: 16_000_000, JPEGQuality: 85}
	}
	opts := parser.PageOptions{Autorotate: true, Layout: true, ReadingOrder: true, Tables: true, Refine: true}
	pipe, err := docpipe.New(docpipe.Config{Renderer: renderer, Render: render, Engine: engine, Options: &opts, Concurrency: cfg.Concurrency})
	if err != nil {
		renderer.Close()
		return nil, err
	}
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	if render == (pdf.RenderOptions{}) {
		render = pdf.RenderOptions{DPI: 300, MaxLongSide: 4000, MaxPixels: 16_000_000, JPEGQuality: 85}
	}
	return &Service{renderer: renderer, render: render, concurrency: concurrency, pipe: pipe, engine: engine, opts: opts, tmpDir: os.TempDir()}, nil
}

// Close releases the PDF renderer.
func (s *Service) Close() { s.renderer.Close() }

// Parse reads f. It fails when the file is not a supported type, the PDF
// cannot be rendered, or ctx is done; a page the engine fails on carries its
// own Error instead.
func (s *Service) Parse(ctx context.Context, f File) (Result, error) {
	switch mt := Detect(f.Data); mt {
	case TypePDF:
		return s.parsePDF(ctx, f.Data)
	case TypeJPEG, TypePNG, TypeTIFF:
		return s.parseImage(ctx, mt, f.Data)
	default:
		return Result{}, ErrUnsupported
	}
}

func (s *Service) parsePDF(ctx context.Context, data []byte) (Result, error) {
	// The renderer reads from a path.
	tmp, err := os.CreateTemp(s.tmpDir, "bepilot-doc-*.pdf")
	if err != nil {
		return Result{}, fmt.Errorf("document: temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return Result{}, fmt.Errorf("document: temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("document: temp file: %w", err)
	}

	res, err := s.pipe.Run(ctx, filepath.Clean(tmp.Name()))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	if err != nil {
		// docpipe fails a run only when the PDF cannot be rendered.
		return Result{}, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	out := Result{MediaType: TypePDF, Pages: make([]Page, len(res.Pages))}
	for i, pg := range res.Pages {
		out.Pages[i] = page(pg.PageNo, pg.Parsed, pg.Raw, pg.OCRErr)
	}
	return out, nil
}

// parseImage reads an image as one page, the way docpipe reads a PDF page.
func (s *Service) parseImage(ctx context.Context, mediaType string, data []byte) (Result, error) {
	img, err := imagefile.Normalize(bytes.NewReader(data), imagefile.Options{MaxPixels: 16_000_000, JPEGQuality: 85})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	raw, err := s.engine.ParsePage(ctx, parser.PageImage{PageNo: 1, Body: bytes.NewReader(img.JPEG), Size: int64(len(img.JPEG)), Width: img.Width, Height: img.Height}, s.opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	return Result{MediaType: mediaType, Pages: []Page{s.readPage(raw, 1, 0, err)}}, nil
}

// readPage assembles the engine output of one page.
func (s *Service) readPage(raw *parser.RawPage, no int, dpi float64, err error) Page {
	if err != nil {
		return page(no, nil, nil, err)
	}
	parsed := assemble.Build(raw, assemble.Options{PageNo: no, DPI: dpi, Engine: s.engine.Name(), ReadingOrderFix: true, LowConfThreshold: 0.6})
	assemble.Render(parsed)
	return page(no, parsed, raw.Raw, nil)
}

// page is what was read from one page. Numbers the VLM and OCR disagree on
// and that were not settled are noted at the end of the markdown.
func page(no int, parsed *types.ParsedPage, raw []byte, err error) Page {
	if err != nil || parsed == nil {
		msg := "page could not be read"
		if err != nil {
			msg = err.Error()
		}
		return Page{PageNo: no, Error: msg}
	}
	p := Page{PageNo: no, Markdown: parsed.Markdown, Text: assemble.PlainText(parsed), Numbers: vlm.PageNumbers(raw)}
	if note := p.Numbers.Note(); note != "" {
		p.Markdown = strings.TrimRight(p.Markdown, "\n") + "\n\n" + note
	}
	return p
}

// EstimateTokens is the approximate number of model tokens of s (about four
// bytes a token, as the agent counts history).
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

// Detect returns the media type of data from its content, or "" when it is
// not one the service reads.
func Detect(data []byte) string {
	if bytes.HasPrefix(data, []byte("II*\x00")) || bytes.HasPrefix(data, []byte("MM\x00*")) {
		return TypeTIFF
	}
	switch mt := http.DetectContentType(data); mt {
	case TypePDF, TypeJPEG, TypePNG:
		return mt
	}
	return ""
}
