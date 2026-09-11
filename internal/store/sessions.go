package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// SessionsRepo persists domain.Session.
type SessionsRepo struct{ pool *pgxpool.Pool }

// CreateParams are the writable fields when creating a session.
type CreateParams struct {
	UserID         uuid.UUID
	Title          string
	Provider       string
	Model          string
	SystemOverride string
	Metadata       map[string]any
}

// Create inserts a new session.
func (r *SessionsRepo) Create(ctx context.Context, p CreateParams) (domain.Session, error) {
	meta := p.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, _ := json.Marshal(meta)

	var s domain.Session
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, title, provider, model, system_override, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, title, provider, model, system_override, summary, metadata, created_at, updated_at`,
		p.UserID, p.Title, p.Provider, p.Model, p.SystemOverride, metaJSON,
	).Scan(&s.ID, &s.UserID, &s.Title, &s.Provider, &s.Model, &s.SystemOverride, &s.Summary, &metaScanner{&s.Metadata}, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("sessions.Create: %w", err)
	}
	return s, nil
}

// Get returns a non-deleted session by id.
func (r *SessionsRepo) Get(ctx context.Context, id uuid.UUID) (domain.Session, error) {
	var s domain.Session
	var deletedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, title, provider, model, system_override, summary, metadata, created_at, updated_at, deleted_at
		FROM sessions WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&s.ID, &s.UserID, &s.Title, &s.Provider, &s.Model, &s.SystemOverride, &s.Summary,
			&metaScanner{&s.Metadata}, &s.CreatedAt, &s.UpdatedAt, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("sessions.Get: %w", err)
	}
	s.DeletedAt = deletedAt
	return s, nil
}

// ListByUser returns sessions for a user, newest first, using updated_at as a
// keyset cursor. A zero `before` starts from the most recent.
func (r *SessionsRepo) ListByUser(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]domain.Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if before.IsZero() {
		before = time.Now().Add(24 * time.Hour)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, title, provider, model, system_override, summary, metadata, created_at, updated_at
		FROM sessions
		WHERE user_id = $1 AND deleted_at IS NULL AND updated_at < $2
		ORDER BY updated_at DESC
		LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("sessions.ListByUser: %w", err)
	}
	defer rows.Close()

	var out []domain.Session
	for rows.Next() {
		var s domain.Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.Title, &s.Provider, &s.Model, &s.SystemOverride,
			&s.Summary, &metaScanner{&s.Metadata}, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateParams carries optional session mutations; nil fields are left as-is.
type UpdateParams struct {
	Title    *string
	Summary  *string
	Metadata map[string]any
}

// Update applies a partial update and bumps updated_at.
func (r *SessionsRepo) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (domain.Session, error) {
	var metaJSON []byte
	if p.Metadata != nil {
		metaJSON, _ = json.Marshal(p.Metadata)
	}
	var s domain.Session
	err := r.pool.QueryRow(ctx, `
		UPDATE sessions SET
			title      = COALESCE($2, title),
			summary    = COALESCE($3, summary),
			metadata   = COALESCE($4, metadata),
			updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, user_id, title, provider, model, system_override, summary, metadata, created_at, updated_at`,
		id, p.Title, p.Summary, metaJSON,
	).Scan(&s.ID, &s.UserID, &s.Title, &s.Provider, &s.Model, &s.SystemOverride, &s.Summary,
		&metaScanner{&s.Metadata}, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("sessions.Update: %w", err)
	}
	return s, nil
}

// Touch bumps updated_at, used after a turn completes.
func (r *SessionsRepo) Touch(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET updated_at = now() WHERE id = $1`, id)
	return err
}

// SoftDelete marks a session deleted.
func (r *SessionsRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `UPDATE sessions SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("sessions.SoftDelete: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// metaScanner unmarshals a jsonb column into a map.
type metaScanner struct{ dst *map[string]any }

func (m *metaScanner) Scan(src any) error {
	*m.dst = map[string]any{}
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		if len(v) == 0 {
			return nil
		}
		return json.Unmarshal(v, m.dst)
	case string:
		if v == "" {
			return nil
		}
		return json.Unmarshal([]byte(v), m.dst)
	default:
		return fmt.Errorf("metaScanner: unsupported type %T", src)
	}
}
