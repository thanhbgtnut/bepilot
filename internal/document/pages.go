package document

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/pdf"
)

// PageSink receives the pages of a file as they are read (ParsePages).
type PageSink struct {
	// Total is called once, before any page, with the page count.
	Total func(pages int) error
	// Page is called for every page read, one call at a time. A page the
	// engine failed on comes with Error set.
	Page func(p Page) error
}

// ParsePages reads f page by page and hands every page to sink as soon as it
// is read, Concurrency pages at a time; the pages in done are skipped (a run
// that resumes). It returns the detected media type. It fails when the file
// is not a supported type, the PDF cannot be opened, ctx is done, or the
// sink returns an error (which stops the run and is returned).
func (s *Service) ParsePages(ctx context.Context, f File, done map[int]bool, sink PageSink) (string, error) {
	mt := Detect(f.Data)
	switch mt {
	case TypePDF:
		return mt, s.parsePDFPages(ctx, f.Data, done, sink)
	case TypeJPEG, TypePNG, TypeTIFF:
		if err := sink.Total(1); err != nil {
			return mt, err
		}
		if done[1] {
			return mt, nil
		}
		res, err := s.parseImage(ctx, mt, f.Data)
		if err != nil {
			return mt, err
		}
		return mt, sink.Page(res.Pages[0])
	default:
		return "", ErrUnsupported
	}
}

func (s *Service) parsePDFPages(ctx context.Context, data []byte, done map[int]bool, sink PageSink) error {
	// The renderer reads from a path.
	tmp, err := os.CreateTemp(s.tmpDir, "bepilot-doc-*.pdf")
	if err != nil {
		return fmt.Errorf("document: temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("document: temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("document: temp file: %w", err)
	}
	path := filepath.Clean(tmp.Name())

	info, err := s.renderer.Probe(ctx, path)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	if err := sink.Total(info.PageCount); err != nil {
		return err
	}
	var todo []int
	for no := 1; no <= info.PageCount; no++ {
		if !done[no] {
			todo = append(todo, no)
		}
	}
	if len(todo) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu      sync.Mutex // serializes sink.Page and guards sinkErr
		sinkErr error
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, s.concurrency)
	// Rendering waits for a free reader before the next page, so at most
	// Concurrency rendered pages are held in memory.
	renderErr := s.renderer.RenderBatch(ctx, path, todo, s.render, func(r pdf.PageRender) error {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			raw, err := s.engine.ParsePage(ctx, parser.PageImage{PageNo: r.PageNo, Body: bytes.NewReader(r.JPEG), Size: int64(len(r.JPEG)), Width: r.Width, Height: r.Height}, s.opts)
			if ctx.Err() != nil {
				return
			}
			p := s.readPage(raw, r.PageNo, r.DPI, err)
			mu.Lock()
			defer mu.Unlock()
			if sinkErr != nil {
				return
			}
			if err := sink.Page(p); err != nil {
				sinkErr = err
				cancel()
			}
		}()
		return nil
	})
	wg.Wait()
	if sinkErr != nil {
		return sinkErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if renderErr != nil {
		return fmt.Errorf("%w: %v", ErrUnreadable, renderErr)
	}
	return nil
}
