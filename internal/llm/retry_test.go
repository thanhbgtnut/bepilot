package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// flakyModel fails its first N calls, then succeeds.
type flakyModel struct {
	failures     int
	generateN    int
	streamN      int
	streamChunks []string
}

func (m *flakyModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *flakyModel) IsCallbacksEnabled() bool { return false }

func (m *flakyModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.generateN++
	if m.generateN <= m.failures {
		return nil, errors.New("transient error")
	}
	return &schema.Message{Role: schema.Assistant, Content: "ok"}, nil
}

func (m *flakyModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamN++
	if m.streamN <= m.failures {
		return nil, errors.New("transient stream error")
	}
	var frames []*schema.Message
	for _, c := range m.streamChunks {
		frames = append(frames, &schema.Message{Role: schema.Assistant, Content: c})
	}
	return schema.StreamReaderFromArray(frames), nil
}

func TestWithRetryGenerateRecoversFromTransientFailure(t *testing.T) {
	inner := &flakyModel{failures: modelRetries} // fails every attempt but the last
	cm := withRetry(inner)
	out, err := cm.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Generate: unexpected error after retries: %v", err)
	}
	if out.Content != "ok" {
		t.Fatalf("Generate: got %q, want %q", out.Content, "ok")
	}
}

func TestWithRetryGenerateGivesUpAfterMaxRetries(t *testing.T) {
	inner := &flakyModel{failures: modelRetries + 1} // never recovers in time
	cm := withRetry(inner)
	if _, err := cm.Generate(context.Background(), nil); err == nil {
		t.Fatal("Generate: expected error after exhausting retries, got nil")
	}
	if inner.generateN != modelRetries+1 {
		t.Fatalf("Generate: called %d times, want %d", inner.generateN, modelRetries+1)
	}
}

func TestWithRetryStreamReturnsFullContentAfterRetry(t *testing.T) {
	inner := &flakyModel{failures: modelRetries, streamChunks: []string{"a", "b", "c"}}
	cm := withRetry(inner)
	stream, err := cm.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream: unexpected error after retries: %v", err)
	}
	defer stream.Close()
	var got string
	for {
		msg, err := stream.Recv()
		if err != nil {
			break
		}
		got += msg.Content
	}
	if got != "abc" {
		t.Fatalf("Stream: got %q, want %q", got, "abc")
	}
}

func TestWithRetryDoesNotRetryAfterContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inner := &flakyModel{failures: modelRetries}
	cm := withRetry(inner)
	if _, err := cm.Generate(ctx, nil); err == nil {
		t.Fatal("Generate: expected error with a cancelled context, got nil")
	}
	if inner.generateN != 1 {
		t.Fatalf("Generate: called %d times with a cancelled context, want 1 (no retries)", inner.generateN)
	}
}
