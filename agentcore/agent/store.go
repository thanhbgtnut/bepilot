package agent

import (
	"context"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/agentcore/domain"
)

// Store is where the agent keeps conversations. The agent never creates or
// deletes sessions; the caller owns them and passes one in on every turn.
//
// Implementations must be safe for concurrent use: a message sent while a turn
// is running is appended to the same session at the same time as the turn's
// own writes. memstore.Store is an in-memory one.
type Store interface {
	// AppendMessage stores m at the end of its session. It sets m.ID, m.Seq
	// (the next number in the session, from 1), m.CreatedAt, and each block's
	// ID, MessageID and Idx. On error it must leave m.ID unset.
	AppendMessage(ctx context.Context, m *domain.Message) error
	// ListMessages returns up to limit messages of a session, oldest first.
	ListMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]domain.Message, error)
	// TouchSession marks the session as active now.
	TouchSession(ctx context.Context, sessionID uuid.UUID) error
	// SetSessionSummary replaces the session's rolling summary.
	SetSessionSummary(ctx context.Context, sessionID uuid.UUID, summary string) error
	// RecordRun stores the observability record of one turn.
	RecordRun(ctx context.Context, run domain.AgentRun) error
}

// SkillRetriever finds the skills relevant to a conversation. skills.Service
// is one. It should not fail a turn: on error it returns no skills.
type SkillRetriever interface {
	Retrieve(ctx context.Context, queryText string, k int) []domain.SkillMatch
}
