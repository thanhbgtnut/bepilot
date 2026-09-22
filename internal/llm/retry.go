package llm

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"time"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// modelRetries is how many extra attempts a chat-model call gets after a
// failure before the turn gives up. Streaming APIs occasionally fail
// mid-stream with a generic, low-detail error — a gateway forwarding a
// backend hiccup, a dropped connection, a momentary overload — that a plain
// retry clears; eino's own newer agent package (adk) defaults to retrying
// any model-call error for that reason. withRetry gives every provider here
// the same resilience without needing to know why any particular call
// failed.
const modelRetries = 2

// withRetry wraps cm so a failed Generate or Stream call is retried with
// backoff before the error reaches the caller. For Stream, the retry is
// resolved before the caller sees anything: the stream is duplicated, one
// copy is drained internally to learn whether the call actually succeeded,
// and only a still-unread copy of a successful call is ever returned — a
// failed attempt never leaks partial chunks to whatever consumes the result.
func withRetry(cm model.ToolCallingChatModel) model.ToolCallingChatModel {
	return &retryModel{inner: cm}
}

type retryModel struct {
	inner model.ToolCallingChatModel
}

func (m *retryModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &retryModel{inner: inner}, nil
}

func (m *retryModel) IsCallbacksEnabled() bool { return components.IsCallbacksEnabled(m.inner) }

func (m *retryModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var lastErr error
	for attempt := 0; attempt <= modelRetries; attempt++ {
		out, err := m.inner.Generate(ctx, in, opts...)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == modelRetries || ctx.Err() != nil || !sleepBackoff(ctx, attempt) {
			break
		}
	}
	return nil, lastErr
}

func (m *retryModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	var lastErr error
	for attempt := 0; attempt <= modelRetries; attempt++ {
		stream, err := m.inner.Stream(ctx, in, opts...)
		if err == nil {
			copies := stream.Copy(2)
			if err = drainForError(copies[0]); err == nil {
				return copies[1], nil
			}
			copies[1].Close()
		}
		lastErr = err
		if attempt == modelRetries || ctx.Err() != nil || !sleepBackoff(ctx, attempt) {
			break
		}
	}
	return nil, lastErr
}

func drainForError(sr *schema.StreamReader[*schema.Message]) error {
	defer sr.Close()
	for {
		_, err := sr.Recv()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

// sleepBackoff waits an exponentially increasing, jittered delay before the
// next attempt. It returns false if ctx is cancelled first, so the caller
// gives up immediately instead of retrying a call nothing is waiting for.
func sleepBackoff(ctx context.Context, attempt int) bool {
	delay := 300 * time.Millisecond * time.Duration(int64(1)<<uint(attempt))
	if delay > 3*time.Second {
		delay = 3 * time.Second
	}
	delay += time.Duration(rand.Int63n(int64(delay)/2 + 1))
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

var (
	_ model.ToolCallingChatModel = (*retryModel)(nil)
	_ components.Checker         = (*retryModel)(nil)
)
