package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type stubTaskResultWriter struct {
	calls []struct {
		sessionID uuid.UUID
		taskKey   string
		title     string
		result    map[string]any
	}
	err error
}

func (w *stubTaskResultWriter) UpsertTaskResult(_ context.Context, sessionID uuid.UUID, taskKey, title string, result map[string]any) error {
	if w.err != nil {
		return w.err
	}
	w.calls = append(w.calls, struct {
		sessionID uuid.UUID
		taskKey   string
		title     string
		result    map[string]any
	}{sessionID, taskKey, title, result})
	return nil
}

func TestSaveTaskResultRequiresARunSession(t *testing.T) {
	w := &stubTaskResultWriter{}
	tl, err := newSaveTaskResultTool(w)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tl.InvokableRun(context.Background(), `{"task_key":"checklist","title":"Checklist","result":{"items":[]}}`)
	if err == nil {
		t.Fatal("expected an error when ctx carries no run session id")
	}
	if len(w.calls) != 0 {
		t.Fatalf("writer should not be called: %+v", w.calls)
	}
}

func TestSaveTaskResultPersistsUnderTheRunSession(t *testing.T) {
	w := &stubTaskResultWriter{}
	tl, err := newSaveTaskResultTool(w)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.New()
	ctx := WithRunSessionID(context.Background(), sessionID)

	out, err := tl.InvokableRun(ctx, `{"task_key":"extraction","title":"Thông tin trích xuất","result":{"fields":[{"label":"Bên vay","value":"Công ty A"}]}}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("want 1 write, got %d", len(w.calls))
	}
	call := w.calls[0]
	if call.sessionID != sessionID {
		t.Errorf("sessionID = %s, want %s", call.sessionID, sessionID)
	}
	if call.taskKey != "extraction" || call.title != "Thông tin trích xuất" {
		t.Errorf("unexpected task_key/title: %+v", call)
	}
	if _, ok := call.result["fields"]; !ok {
		t.Errorf("result missing 'fields': %+v", call.result)
	}
	if out == "" {
		t.Error("expected a non-empty result payload")
	}
}

func TestSaveTaskResultRejectsIncompleteArgs(t *testing.T) {
	w := &stubTaskResultWriter{}
	tl, err := newSaveTaskResultTool(w)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRunSessionID(context.Background(), uuid.New())

	cases := []string{
		`{"task_key":"","title":"x","result":{"a":1}}`,
		`{"task_key":"x","title":"","result":{"a":1}}`,
		`{"task_key":"x","title":"y","result":{}}`,
	}
	for _, args := range cases {
		if _, err := tl.InvokableRun(ctx, args); err == nil {
			t.Errorf("args %s: expected an error", args)
		}
	}
	if len(w.calls) != 0 {
		t.Fatalf("no writes expected for invalid args: %+v", w.calls)
	}
}

func TestSaveTaskResultWithoutAWriterReportsUnavailable(t *testing.T) {
	tl, err := newSaveTaskResultTool(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRunSessionID(context.Background(), uuid.New())
	if _, err := tl.InvokableRun(ctx, `{"task_key":"x","title":"y","result":{"a":1}}`); err == nil {
		t.Fatal("expected an error when no writer is configured")
	}
}
