package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// ErrLeaseLost is returned to a worker whose hold on a document ran out (and
// another worker may have taken it) or whose document was deleted.
var ErrLeaseLost = errors.New("document lease lost")

// LibraryRepo persists uploaded documents (/v1/documents), their pages and
// the sessions they are attached to. The library_documents table is also the queue
// of the background readers.
type LibraryRepo struct{ pool *pgxpool.Pool }

const documentCols = `id, user_id, name, media_type, size_bytes, sha256, status, pages_total, pages_done,
	failed_pages, unverified_numbers, token_count, error, attempts, finished_at, created_at, updated_at`

func scanDocument(row pgx.Row) (domain.Document, error) {
	var d domain.Document
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.MediaType, &d.Size, &d.SHA256, &d.Status, &d.PagesTotal, &d.PagesDone,
		&d.FailedPages, &d.UnverifiedNumbers, &d.TokenCount, &d.Error, &d.Attempts, &d.FinishedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Document{}, ErrNotFound
	}
	return d, err
}

// Create stores an uploaded file as a pending document, unless the user
// already has the same content: then that document is returned (created is
// false), and one that failed is queued again.
func (r *LibraryRepo) Create(ctx context.Context, d *domain.Document) (created bool, err error) {
	sum := sha256.Sum256(d.Data)
	d.SHA256, d.Size = hex.EncodeToString(sum[:]), int64(len(d.Data))
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("library.Create: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// One upload at a time per user and content.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, d.UserID.String()+d.SHA256); err != nil {
		return false, fmt.Errorf("library.Create: lock: %w", err)
	}
	existing, err := scanDocument(tx.QueryRow(ctx, `SELECT `+documentCols+` FROM library_documents
		WHERE user_id = $1 AND sha256 = $2 ORDER BY created_at DESC LIMIT 1`, d.UserID, d.SHA256))
	switch {
	case err == nil && existing.Status == domain.DocumentFailed:
		existing, err = scanDocument(tx.QueryRow(ctx, `UPDATE library_documents SET status = 'pending', error = '', attempts = 0,
			run_after = now(), lease_token = NULL, locked_until = NULL, finished_at = NULL, updated_at = now()
			WHERE id = $1 RETURNING `+documentCols, existing.ID))
		if err != nil {
			return false, fmt.Errorf("library.Create: requeue: %w", err)
		}
		*d = existing
		return false, tx.Commit(ctx)
	case err == nil:
		*d = existing
		return false, tx.Commit(ctx)
	case !errors.Is(err, ErrNotFound):
		return false, fmt.Errorf("library.Create: find: %w", err)
	}

	row := tx.QueryRow(ctx, `INSERT INTO library_documents (user_id, name, media_type, size_bytes, sha256, data)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+documentCols,
		d.UserID, cleanText(d.Name), cleanText(d.MediaType), d.Size, d.SHA256, d.Data)
	saved, err := scanDocument(row)
	if err != nil {
		return false, fmt.Errorf("library.Create: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("library.Create: commit: %w", err)
	}
	*d = saved
	return true, nil
}

// Get returns a document without its file.
func (r *LibraryRepo) Get(ctx context.Context, id uuid.UUID) (domain.Document, error) {
	d, err := scanDocument(r.pool.QueryRow(ctx, `SELECT `+documentCols+` FROM library_documents WHERE id = $1`, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return d, fmt.Errorf("library.Get: %w", err)
	}
	return d, err
}

// ListByUser returns a user's documents, newest first, created before
// before (zero: no bound).
func (r *LibraryRepo) ListByUser(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]domain.Document, error) {
	if before.IsZero() {
		before = time.Now().Add(time.Hour)
	}
	rows, err := r.pool.Query(ctx, `SELECT `+documentCols+` FROM library_documents
		WHERE user_id = $1 AND created_at < $2 ORDER BY created_at DESC LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("library.ListByUser: %w", err)
	}
	return collectDocuments(rows)
}

func collectDocuments(rows pgx.Rows) ([]domain.Document, error) {
	defer rows.Close()
	var out []domain.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delete removes a document with its pages and session links.
func (r *LibraryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM library_documents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("library.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Pages returns the pages from..to (inclusive; 0 = no bound) in order.
func (r *LibraryRepo) Pages(ctx context.Context, id uuid.UUID, from, to int) ([]domain.DocumentPage, error) {
	if to <= 0 {
		to = 1 << 30
	}
	rows, err := r.pool.Query(ctx, `SELECT document_id, page_no, markdown, ocr_text, numbers, unverified, error, tokens
		FROM library_pages WHERE document_id = $1 AND page_no BETWEEN $2 AND $3 ORDER BY page_no`, id, from, to)
	if err != nil {
		return nil, fmt.Errorf("library.Pages: %w", err)
	}
	defer rows.Close()
	var out []domain.DocumentPage
	for rows.Next() {
		var p domain.DocumentPage
		var numbers []byte
		if err := rows.Scan(&p.DocumentID, &p.PageNo, &p.Markdown, &p.OCRText, &numbers, &p.Unverified, &p.Error, &p.Tokens); err != nil {
			return nil, err
		}
		if numbers != nil {
			p.Numbers = json.RawMessage(numbers)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- sessions ----------------------------------------------------------------

// Attach links documents to a session; documents already linked keep their
// place.
func (r *LibraryRepo) Attach(ctx context.Context, sessionID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if _, err := r.pool.Exec(ctx, `INSERT INTO session_documents (session_id, document_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, sessionID, id); err != nil {
			return fmt.Errorf("library.Attach: %w", err)
		}
	}
	return nil
}

// SessionDocuments returns the documents of a session in the order they were
// attached.
func (r *LibraryRepo) SessionDocuments(ctx context.Context, sessionID uuid.UUID) ([]domain.Document, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.id, d.user_id, d.name, d.media_type, d.size_bytes, d.sha256, d.status,
		d.pages_total, d.pages_done, d.failed_pages, d.unverified_numbers, d.token_count, d.error, d.attempts, d.finished_at,
		d.created_at, d.updated_at
		FROM session_documents s JOIN library_documents d ON d.id = s.document_id
		WHERE s.session_id = $1 ORDER BY s.attached_at, d.id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("library.SessionDocuments: %w", err)
	}
	return collectDocuments(rows)
}

// --- queue -------------------------------------------------------------------

// Claim takes the oldest document that is waiting, or whose worker's lease
// ran out, and holds it for lease. It returns the document with its file
// and the lease token every later call must pass, or ErrNotFound when
// nothing is waiting.
func (r *LibraryRepo) Claim(ctx context.Context, lease time.Duration) (domain.Document, uuid.UUID, error) {
	token := uuid.New()
	var data []byte
	row := r.pool.QueryRow(ctx, `UPDATE library_documents SET status = 'processing', attempts = attempts + 1,
			lease_token = $1, locked_until = now() + make_interval(secs => $2), updated_at = now()
		WHERE id = (SELECT id FROM library_documents
			WHERE (status = 'pending' AND run_after <= now()) OR (status = 'processing' AND locked_until < now())
			ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING `+documentCols+`, data`, token, lease.Seconds())
	var d domain.Document
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.MediaType, &d.Size, &d.SHA256, &d.Status, &d.PagesTotal, &d.PagesDone,
		&d.FailedPages, &d.UnverifiedNumbers, &d.TokenCount, &d.Error, &d.Attempts, &d.FinishedAt, &d.CreatedAt, &d.UpdatedAt, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Document{}, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return domain.Document{}, uuid.Nil, fmt.Errorf("library.Claim: %w", err)
	}
	d.Data = data
	return d, token, nil
}

// leased runs an update that applies only while token holds the lease.
func (r *LibraryRepo) leased(ctx context.Context, op, sql string, args ...any) error {
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("library.%s: %w", op, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Renew extends the lease.
func (r *LibraryRepo) Renew(ctx context.Context, id, token uuid.UUID, lease time.Duration) error {
	return r.leased(ctx, "Renew", `UPDATE library_documents SET locked_until = now() + make_interval(secs => $3)
		WHERE id = $1 AND lease_token = $2`, id, token, lease.Seconds())
}

// SetTotal records the page count.
func (r *LibraryRepo) SetTotal(ctx context.Context, id, token uuid.UUID, pages int, mediaType string) error {
	return r.leased(ctx, "SetTotal", `UPDATE library_documents SET pages_total = $3, media_type = $4, updated_at = now()
		WHERE id = $1 AND lease_token = $2`, id, token, pages, mediaType)
}

// DonePages returns the page numbers already read. A page the engine failed
// on is not done: the next attempt reads it again.
func (r *LibraryRepo) DonePages(ctx context.Context, id uuid.UUID) (map[int]bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT page_no FROM library_pages WHERE document_id = $1 AND error = ''`, id)
	if err != nil {
		return nil, fmt.Errorf("library.DonePages: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// SavePage stores one page and moves the progress on.
func (r *LibraryRepo) SavePage(ctx context.Context, token uuid.UUID, p domain.DocumentPage) error {
	var numbers []byte
	if p.Numbers != nil {
		var err error
		if numbers, err = cleanJSON(p.Numbers); err != nil {
			return fmt.Errorf("library.SavePage marshal: %w", err)
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("library.SavePage: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	// Lock the row first: a lost lease or a deleted document stops here.
	var held bool
	err = tx.QueryRow(ctx, `SELECT true FROM library_documents WHERE id = $1 AND lease_token = $2 FOR UPDATE`, p.DocumentID, token).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("library.SavePage: lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO library_pages (document_id, page_no, markdown, ocr_text, numbers, unverified, error, tokens)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (document_id, page_no) DO UPDATE SET markdown = EXCLUDED.markdown, ocr_text = EXCLUDED.ocr_text,
			numbers = EXCLUDED.numbers, unverified = EXCLUDED.unverified, error = EXCLUDED.error, tokens = EXCLUDED.tokens`,
		p.DocumentID, p.PageNo, cleanText(p.Markdown), cleanText(p.OCRText), nullableJSON(numbers), p.Unverified, cleanText(p.Error), p.Tokens); err != nil {
		return fmt.Errorf("library.SavePage: insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE library_documents SET
			pages_done = (SELECT count(*) FROM library_pages WHERE document_id = $1), updated_at = now()
		WHERE id = $1`, p.DocumentID); err != nil {
		return fmt.Errorf("library.SavePage: progress: %w", err)
	}
	return tx.Commit(ctx)
}

// Finish marks the document ready, with totals summed over its pages.
func (r *LibraryRepo) Finish(ctx context.Context, id, token uuid.UUID) error {
	return r.leased(ctx, "Finish", `UPDATE library_documents d SET status = 'ready', error = '',
			pages_done = s.n, failed_pages = s.failed, unverified_numbers = s.unverified, token_count = s.tokens,
			lease_token = NULL, locked_until = NULL, finished_at = now(), updated_at = now()
		FROM (SELECT count(*) AS n, count(*) FILTER (WHERE error <> '') AS failed,
				coalesce(sum(unverified), 0) AS unverified, coalesce(sum(tokens), 0) AS tokens
			FROM library_pages WHERE document_id = $1) s
		WHERE d.id = $1 AND d.lease_token = $2`, id, token)
}

// Fail records a failed attempt: the document is queued again after
// retryAfter, or marked failed when final.
func (r *LibraryRepo) Fail(ctx context.Context, id, token uuid.UUID, msg string, retryAfter time.Duration, final bool) error {
	status, finished := domain.DocumentPending, "NULL"
	if final {
		status, finished = domain.DocumentFailed, "now()"
	}
	return r.leased(ctx, "Fail", `UPDATE library_documents SET status = $3, error = $4, run_after = now() + make_interval(secs => $5),
			lease_token = NULL, locked_until = NULL, finished_at = `+finished+`, updated_at = now()
		WHERE id = $1 AND lease_token = $2`, id, token, status, cleanText(msg), retryAfter.Seconds())
}

// Release gives a document back to the queue without counting the attempt
// (the worker is shutting down).
func (r *LibraryRepo) Release(ctx context.Context, id, token uuid.UUID) error {
	return r.leased(ctx, "Release", `UPDATE library_documents SET status = 'pending', attempts = greatest(attempts - 1, 0),
			lease_token = NULL, locked_until = NULL, updated_at = now()
		WHERE id = $1 AND lease_token = $2`, id, token)
}
