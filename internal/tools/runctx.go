package tools

import (
	"context"

	"github.com/google/uuid"
)

type runSessionIDKey struct{}

// WithRunSessionID attaches the bepilot session id driving the current turn
// to ctx. Built-in tools that persist per-session data (e.g. save_task_result)
// read it back with RunSessionIDFrom instead of trusting the model to copy a
// UUID into its arguments correctly.
func WithRunSessionID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, runSessionIDKey{}, id)
}

// RunSessionIDFrom returns the session id attached by WithRunSessionID.
func RunSessionIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(runSessionIDKey{}).(uuid.UUID)
	return id, ok
}
