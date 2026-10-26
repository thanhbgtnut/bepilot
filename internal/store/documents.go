package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// DocumentsRepo persists domain.DocumentRequest and its files.
type DocumentsRepo struct{ pool *pgxpool.Pool }

// Create stores a new request (status "processing") with its files in one
// transaction. It sets the IDs, sizes and checksums of req and files.
func (r *DocumentsRepo) Create(ctx context.Context, req *domain.DocumentRequest, files []domain.DocumentFile) error {
	reqJSON, err := cleanJSON(req.Request)
	if err != nil {
		return fmt.Errorf("documents.Create marshal: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("documents.Create: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	req.Status = domain.DocumentProcessing
	if err := tx.QueryRow(ctx, `
		INSERT INTO document_requests (user_id, kind, status, request)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		req.UserID, req.Kind, req.Status, reqJSON,
	).Scan(&req.ID, &req.CreatedAt, &req.UpdatedAt); err != nil {
		return fmt.Errorf("documents.Create: insert request: %w", err)
	}
	for i := range files {
		f := &files[i]
		sum := sha256.Sum256(f.Data)
		f.RequestID, f.Idx, f.Size, f.SHA256 = req.ID, i, int64(len(f.Data)), hex.EncodeToString(sum[:])
		if err := tx.QueryRow(ctx, `
			INSERT INTO document_files (request_id, idx, name, media_type, size_bytes, sha256, data)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id`,
			f.RequestID, f.Idx, cleanText(f.Name), cleanText(f.MediaType), f.Size, f.SHA256, f.Data,
		).Scan(&f.ID); err != nil {
			return fmt.Errorf("documents.Create: insert file %d: %w", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("documents.Create: commit: %w", err)
	}
	return nil
}

// Finish records how a request ended: its status, docpipe result, response,
// error, and the session and messages it produced. latency_ms is the time
// since the request was created.
func (r *DocumentsRepo) Finish(ctx context.Context, req domain.DocumentRequest) error {
	var result, response []byte
	var err error
	if req.Result != nil {
		if result, err = cleanJSON(req.Result); err != nil {
			return fmt.Errorf("documents.Finish marshal result: %w", err)
		}
	}
	if req.Response != nil {
		if response, err = cleanJSON(req.Response); err != nil {
			return fmt.Errorf("documents.Finish marshal response: %w", err)
		}
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE document_requests SET
			status = $2, result = $3, response = $4, error = $5,
			session_id = $6, user_message_id = $7, assistant_message_id = $8,
			latency_ms = (EXTRACT(EPOCH FROM now() - created_at) * 1000)::integer, updated_at = now()
		WHERE id = $1`,
		req.ID, req.Status, nullableJSON(result), nullableJSON(response), cleanText(req.Error),
		req.SessionID, req.UserMessageID, req.AssistantMessageID)
	if err != nil {
		return fmt.Errorf("documents.Finish: %w", err)
	}
	return nil
}

// Get returns one request (without its files).
func (r *DocumentsRepo) Get(ctx context.Context, id uuid.UUID) (domain.DocumentRequest, error) {
	var d domain.DocumentRequest
	var result, response []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, kind, status, request, result, response, error,
			session_id, user_message_id, assistant_message_id, latency_ms, created_at, updated_at
		FROM document_requests WHERE id = $1`, id,
	).Scan(&d.ID, &d.UserID, &d.Kind, &d.Status, &metaScanner{&d.Request}, &result, &response, &d.Error,
		&d.SessionID, &d.UserMessageID, &d.AssistantMessageID, &d.LatencyMS, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DocumentRequest{}, ErrNotFound
	}
	if err != nil {
		return domain.DocumentRequest{}, fmt.Errorf("documents.Get: %w", err)
	}
	if result != nil {
		d.Result = json.RawMessage(result)
	}
	if response != nil {
		d.Response = json.RawMessage(response)
	}
	return d, nil
}

// Files returns a request's files in upload order.
func (r *DocumentsRepo) Files(ctx context.Context, requestID uuid.UUID) ([]domain.DocumentFile, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, request_id, idx, name, media_type, size_bytes, sha256, data
		FROM document_files WHERE request_id = $1 ORDER BY idx`, requestID)
	if err != nil {
		return nil, fmt.Errorf("documents.Files: %w", err)
	}
	defer rows.Close()
	var out []domain.DocumentFile
	for rows.Next() {
		var f domain.DocumentFile
		if err := rows.Scan(&f.ID, &f.RequestID, &f.Idx, &f.Name, &f.MediaType, &f.Size, &f.SHA256, &f.Data); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
