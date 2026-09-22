package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"
)

// TaskResultWriter persists one skill task's structured output, keyed by
// session and task_key (a second call with the same task_key on the same
// session replaces the first). It is a narrow interface — not the store
// package itself — so a skill's report data can be saved without this
// package depending on internal/store.
type TaskResultWriter interface {
	UpsertTaskResult(ctx context.Context, sessionID uuid.UUID, taskKey, title string, result map[string]any) error
}

// --- save_task_result -------------------------------------------------—-—-

type saveTaskResultArgs struct {
	TaskKey string         `json:"task_key" jsonschema:"required" jsonschema_description:"Short, stable id for this task within the current skill, e.g. 'checklist' or 'extraction'. Calling again with the same task_key on this session replaces the previous result."`
	Title   string         `json:"title" jsonschema:"required" jsonschema_description:"Short human-readable title for this result, shown as-is in the client's report view."`
	Result  map[string]any `json:"result" jsonschema:"required" jsonschema_description:"The task's structured output as a JSON object, in exactly the shape the active skill's instructions describe for this task."`
}

// newSaveTaskResultTool builds save_task_result. w may be nil (task result
// storage not configured); the tool then reports that clearly instead of
// panicking.
func newSaveTaskResultTool(w TaskResultWriter) (tool.InvokableTool, error) {
	return utils.InferTool(
		"save_task_result",
		"Persist one task's structured result for the current session so a client UI can render it (e.g. a 'full report' view) independently of this conversation. Call once per task, right after you present that task's result, using the exact JSON shape the active skill's instructions specify — do not invent a shape. The session id is taken from the running turn; never pass one yourself.",
		func(ctx context.Context, a saveTaskResultArgs) (map[string]any, error) {
			if w == nil {
				return nil, fmt.Errorf("task result storage is not configured on this deployment")
			}
			sessionID, ok := RunSessionIDFrom(ctx)
			if !ok {
				return nil, fmt.Errorf("no active session for this run")
			}
			taskKey := strings.TrimSpace(a.TaskKey)
			title := strings.TrimSpace(a.Title)
			if taskKey == "" {
				return nil, fmt.Errorf("task_key is required")
			}
			if title == "" {
				return nil, fmt.Errorf("title is required")
			}
			if len(a.Result) == 0 {
				return nil, fmt.Errorf("result must not be empty")
			}
			if err := w.UpsertTaskResult(ctx, sessionID, taskKey, title, a.Result); err != nil {
				return nil, fmt.Errorf("save_task_result: %w", err)
			}
			return map[string]any{"saved": true, "task_key": taskKey}, nil
		},
	)
}
