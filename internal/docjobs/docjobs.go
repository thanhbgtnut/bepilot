// Package docjobs reads uploaded documents in the background. Workers take
// documents from the library_documents table (store.LibraryRepo is the queue), read
// them page by page with the document service and store every page as soon
// as it is read, so a client sees the progress and a worker that stops
// half way leaves only the missing pages to the next one.
package docjobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/document"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/store"
)

// Pool runs the workers.
type Pool struct {
	lib  *store.LibraryRepo
	svc  *document.Service
	cfg  config.Documents
	log  *slog.Logger
	wake chan struct{}
}

// New returns a pool; Run starts it.
func New(lib *store.LibraryRepo, svc *document.Service, cfg config.Documents, log *slog.Logger) *Pool {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Pool{lib: lib, svc: svc, cfg: cfg, log: log, wake: make(chan struct{}, max(cfg.Workers, 1))}
}

// Notify wakes an idle worker: a document was just uploaded.
func (p *Pool) Notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run starts the workers and returns when ctx is done and every worker has
// handed its document back.
func (p *Pool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < max(p.cfg.Workers, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.loop(ctx)
		}()
	}
	wg.Wait()
}

func (p *Pool) loop(ctx context.Context) {
	poll := p.cfg.PollInterval
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for ctx.Err() == nil {
		d, token, err := p.lib.Claim(ctx, p.cfg.Lease)
		if err == nil {
			p.process(ctx, d, token)
			continue
		}
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			p.log.Error("document queue: claim failed", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-p.wake:
		case <-time.After(poll):
		}
	}
}

// errPermanent marks a failure a retry cannot fix.
var errPermanent = errors.New("permanent")

// process reads one claimed document.
func (p *Pool) process(ctx context.Context, d domain.Document, token uuid.UUID) {
	log := p.log.With("document", d.ID, "attempt", d.Attempts)
	log.Info("document reading started", "pages_done", d.PagesDone)
	t0 := time.Now()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go p.heartbeat(runCtx, cancel, d.ID, token, log)

	err := p.read(runCtx, d, token)
	// Bookkeeping must not depend on the run's context, which may be done.
	bctx, bcancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer bcancel()

	switch {
	case ctx.Err() != nil:
		// Shutting down: hand the document back, the pages read so far stay.
		if err := p.lib.Release(bctx, d.ID, token); err != nil && !errors.Is(err, store.ErrLeaseLost) {
			log.Error("document release failed", "err", err)
		}
		log.Info("document handed back on shutdown")
	case errors.Is(err, store.ErrLeaseLost):
		// Deleted, or another worker took it over.
		log.Info("document reading stopped: lease lost")
	case err != nil:
		final := errors.Is(err, errPermanent) || d.Attempts >= max(p.cfg.MaxAttempts, 1)
		retry := backoff(d.Attempts)
		if ferr := p.lib.Fail(bctx, d.ID, token, err.Error(), retry, final); ferr != nil && !errors.Is(ferr, store.ErrLeaseLost) {
			log.Error("document failure not recorded", "err", ferr)
		}
		log.Warn("document reading failed", "err", err, "final", final, "retry_in", retry)
	default:
		if ferr := p.lib.Finish(bctx, d.ID, token); ferr != nil && !errors.Is(ferr, store.ErrLeaseLost) {
			log.Error("document finish not recorded", "err", ferr)
			return
		}
		log.Info("document ready", "ms", time.Since(t0).Milliseconds())
	}
}

// read reads the pages the document does not have yet. A page that failed
// on an earlier attempt is read again.
func (p *Pool) read(ctx context.Context, d domain.Document, token uuid.UUID) error {
	done, err := p.lib.DonePages(ctx, d.ID)
	if err != nil {
		return err
	}
	mt := document.Detect(d.Data)
	read, failed := 0, 0
	var firstErr string
	_, err = p.svc.ParsePages(ctx, document.File{Name: d.Name, MediaType: d.MediaType, Data: d.Data}, done, document.PageSink{
		Total: func(n int) error { return p.lib.SetTotal(ctx, d.ID, token, n, mt) },
		Page: func(pg document.Page) error {
			read++
			if pg.Error != "" {
				failed++
				if firstErr == "" {
					firstErr = pg.Error
				}
			}
			return p.lib.SavePage(ctx, token, domain.DocumentPage{
				DocumentID: d.ID, PageNo: pg.PageNo, Markdown: pg.Markdown, OCRText: pg.Text,
				Numbers: numbers(pg), Unverified: pg.Unverified(), Error: pg.Error, Tokens: document.EstimateTokens(pg.Markdown),
			})
		},
	})
	switch {
	case errors.Is(err, document.ErrUnsupported), errors.Is(err, document.ErrUnreadable):
		return fmt.Errorf("%w: %w", errPermanent, err)
	case err != nil:
		return err
	case len(done) == 0 && read > 0 && failed == read:
		// Nothing of the file could be read: the OCR engine is likely down.
		return fmt.Errorf("no page of the document could be read: %s", firstErr)
	case failed > 0:
		// Retry the failed pages while attempts remain; the document is
		// ready with them marked otherwise.
		if d.Attempts < max(p.cfg.MaxAttempts, 1) {
			return fmt.Errorf("%d of %d pages could not be read: %s", failed, read, firstErr)
		}
	}
	return nil
}

func numbers(pg document.Page) any {
	if pg.Numbers == nil {
		return nil
	}
	return pg.Numbers
}

// heartbeat renews the lease while the document is read, and stops the run
// when the lease is lost.
func (p *Pool) heartbeat(ctx context.Context, stop context.CancelFunc, id, token uuid.UUID, log *slog.Logger) {
	every := max(p.cfg.Lease/3, time.Second)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := p.lib.Renew(ctx, id, token, p.cfg.Lease)
			if errors.Is(err, store.ErrLeaseLost) {
				log.Info("document lease lost; stopping")
				stop()
				return
			}
			if err != nil && ctx.Err() == nil {
				log.Warn("document lease renewal failed", "err", err)
			}
		}
	}
}

// backoff is the wait before attempt n+1: 10s, 20s, 40s… capped at 5m.
func backoff(attempt int) time.Duration {
	d := 10 * time.Second << min(max(attempt-1, 0), 5)
	return min(d, 5*time.Minute)
}
