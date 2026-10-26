package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/agentcore/agent"
	"github.com/thanhenti/bepilot/internal/domain"
)

// Agent returns the store as the agent sees it.
func (s *Store) Agent() agent.Store { return agentStore{s} }

// agentStore implements agent.Store on the Postgres repositories.
type agentStore struct{ s *Store }

func (a agentStore) AppendMessage(ctx context.Context, m *domain.Message) error {
	return a.s.Messages.Append(ctx, m)
}

func (a agentStore) ListMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]domain.Message, error) {
	return a.s.Messages.ListBySession(ctx, sessionID, 0, limit)
}

func (a agentStore) TouchSession(ctx context.Context, sessionID uuid.UUID) error {
	return a.s.Sessions.Touch(ctx, sessionID)
}

func (a agentStore) SetSessionSummary(ctx context.Context, sessionID uuid.UUID, summary string) error {
	_, err := a.s.Sessions.Update(ctx, sessionID, UpdateParams{Summary: &summary})
	return err
}

func (a agentStore) RecordRun(ctx context.Context, run domain.AgentRun) error {
	return a.s.Runs.Insert(ctx, run)
}
