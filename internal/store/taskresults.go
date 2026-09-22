package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// TaskResultsRepo persists domain.TaskResult, independent of the message
// transcript and of the agent package: a report UI reads through here, not
// through a run, so it keeps working even when the agent pipeline is broken.
type TaskResultsRepo struct{ pool *pgxpool.Pool }

// Upsert inserts or replaces a session's result for the given task_key.
func (r *TaskResultsRepo) Upsert(ctx context.Context, sessionID uuid.UUID, taskKey, title string, result map[string]any) (domain.TaskResult, error) {
	resultJSON, err := cleanJSON(result)
	if err != nil {
		return domain.TaskResult{}, fmt.Errorf("task_results.Upsert marshal: %w", err)
	}
	var t domain.TaskResult
	err = r.pool.QueryRow(ctx, `
		INSERT INTO task_results (session_id, task_key, title, result)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (session_id, task_key) DO UPDATE
		SET title = EXCLUDED.title, result = EXCLUDED.result, updated_at = now()
		RETURNING id, session_id, task_key, title, result, created_at, updated_at`,
		sessionID, taskKey, title, resultJSON,
	).Scan(&t.ID, &t.SessionID, &t.TaskKey, &t.Title, &metaScanner{&t.Result}, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return domain.TaskResult{}, fmt.Errorf("task_results.Upsert: %w", err)
	}
	return t, nil
}

// UpsertTaskResult adapts Upsert to tools.TaskResultWriter (the built-in
// save_task_result tool depends only on that narrow interface, not on this
// package, so internal/tools never has to import internal/store).
func (r *TaskResultsRepo) UpsertTaskResult(ctx context.Context, sessionID uuid.UUID, taskKey, title string, result map[string]any) error {
	_, err := r.Upsert(ctx, sessionID, taskKey, title, result)
	return err
}

// ListBySession returns a session's task results, oldest first.
func (r *TaskResultsRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]domain.TaskResult, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, task_key, title, result, created_at, updated_at
		FROM task_results WHERE session_id = $1 ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("task_results.ListBySession: %w", err)
	}
	defer rows.Close()

	var out []domain.TaskResult
	for rows.Next() {
		var t domain.TaskResult
		if err := rows.Scan(&t.ID, &t.SessionID, &t.TaskKey, &t.Title, &metaScanner{&t.Result}, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
