package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// AgentRunsRepo persists one observability row per assistant turn.
type AgentRunsRepo struct{ pool *pgxpool.Pool }

// Insert writes an agent run record.
func (r *AgentRunsRepo) Insert(ctx context.Context, run domain.AgentRun) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO agent_runs
			(session_id, message_id, provider, model, steps, tokens_in, tokens_out, latency_ms, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		run.SessionID, run.MessageID, run.Provider, run.Model,
		run.Steps, run.TokensIn, run.TokensOut, run.LatencyMS, run.Error)
	if err != nil {
		return fmt.Errorf("agentruns.Insert: %w", err)
	}
	return nil
}
