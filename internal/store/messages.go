package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// MessagesRepo persists messages and their content blocks, and reconstructs
// conversation transcripts.
type MessagesRepo struct{ pool *pgxpool.Pool }

// Append writes one message and its ordered content blocks in a single
// transaction, assigning the next sequence number for the session.
func (r *MessagesRepo) Append(ctx context.Context, m *domain.Message) (err error) {
	// The transaction below hands out ids (RETURNING) before it can still fail.
	// If it does, the rows are rolled back, so the caller must not be left
	// holding ids of records that do not exist — anything that references the
	// message (agent_runs.message_id) would violate its foreign key.
	defer func() {
		if err != nil {
			m.ID, m.Seq = uuid.Nil, 0
			for i := range m.Blocks {
				m.Blocks[i].ID, m.Blocks[i].MessageID = uuid.Nil, uuid.Nil
			}
		}
	}()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("messages.Append: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialise writers per session. Without this two concurrent Appends (a
	// message sent while a turn is still running) both read the same MAX(seq)
	// and either collide on the unique key or interleave out of order. The lock
	// is held until this transaction ends.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, m.SessionID.String()); err != nil {
		return fmt.Errorf("messages.Append: lock: %w", err)
	}

	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = $1`, m.SessionID,
	).Scan(&seq); err != nil {
		return fmt.Errorf("messages.Append: seq: %w", err)
	}
	m.Seq = seq

	usageJSON, _ := json.Marshal(m.Usage)
	m.Role = cleanText(m.Role)
	m.StopReason = cleanText(m.StopReason)
	if err := tx.QueryRow(ctx, `
		INSERT INTO messages (session_id, seq, role, stop_reason, usage)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		m.SessionID, m.Seq, m.Role, m.StopReason, usageJSON,
	).Scan(&m.ID, &m.CreatedAt); err != nil {
		return fmt.Errorf("messages.Append: insert message: %w", err)
	}

	for i := range m.Blocks {
		b := &m.Blocks[i]
		b.MessageID = m.ID
		b.Idx = i
		var toolInput, toolResult []byte
		if b.ToolInput != nil {
			toolInput, _ = cleanJSON(b.ToolInput)
		}
		if b.ToolResult != nil {
			toolResult, _ = cleanJSON(b.ToolResult)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO content_blocks
				(message_id, idx, type, text, thinking, tool_name, tool_use_id, tool_input, tool_result, is_error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id`,
			b.MessageID, b.Idx, cleanText(b.Type), cleanText(b.Text), cleanText(b.Thinking),
			cleanText(b.ToolName), cleanText(b.ToolUseID),
			nullableJSON(toolInput), nullableJSON(toolResult), b.IsError,
		).Scan(&b.ID); err != nil {
			return fmt.Errorf("messages.Append: insert block %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("messages.Append: commit: %w", err)
	}
	return nil
}

// ListBySession returns messages (with blocks) ordered by seq ascending.
// A `afterSeq` of 0 returns from the start.
func (r *MessagesRepo) ListBySession(ctx context.Context, sessionID uuid.UUID, afterSeq, limit int) ([]domain.Message, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, seq, role, stop_reason, usage, created_at
		FROM messages
		WHERE session_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("messages.ListBySession: %w", err)
	}
	defer rows.Close()

	var msgs []domain.Message
	byID := map[uuid.UUID]*domain.Message{}
	for rows.Next() {
		var m domain.Message
		var usageJSON []byte
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Seq, &m.Role, &m.StopReason, &usageJSON, &m.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(usageJSON, &m.Usage)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	for i := range msgs {
		byID[msgs[i].ID] = &msgs[i]
	}

	ids := make([]uuid.UUID, len(msgs))
	for i := range msgs {
		ids[i] = msgs[i].ID
	}
	brows, err := r.pool.Query(ctx, `
		SELECT id, message_id, idx, type, text, thinking, tool_name, tool_use_id, tool_input, tool_result, is_error
		FROM content_blocks
		WHERE message_id = ANY($1)
		ORDER BY message_id, idx ASC`, ids)
	if err != nil {
		return nil, fmt.Errorf("messages.ListBySession: blocks: %w", err)
	}
	defer brows.Close()
	for brows.Next() {
		var b domain.ContentBlock
		var toolInput, toolResult []byte
		if err := brows.Scan(&b.ID, &b.MessageID, &b.Idx, &b.Type, &b.Text, &b.Thinking,
			&b.ToolName, &b.ToolUseID, &toolInput, &toolResult, &b.IsError); err != nil {
			return nil, err
		}
		if len(toolInput) > 0 {
			_ = json.Unmarshal(toolInput, &b.ToolInput)
		}
		if len(toolResult) > 0 {
			_ = json.Unmarshal(toolResult, &b.ToolResult)
		}
		if m := byID[b.MessageID]; m != nil {
			m.Blocks = append(m.Blocks, b)
		}
	}
	return msgs, brows.Err()
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

var _ = pgx.ErrNoRows
