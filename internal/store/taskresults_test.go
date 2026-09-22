package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/config"
)

// TestTaskResultsUpsertReplacesByTaskKey needs a Postgres (TEST_DATABASE_URL).
// It writes only its own user/session and removes them (cascading to
// task_results) afterwards, so it is safe to point at a development database.
func TestTaskResultsUpsertReplacesByTaskKey(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := Open(ctx, config.DB{DSN: dsn, MaxConns: 2, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	user, err := st.Users.Create(ctx, fmt.Sprintf("store-test-%s@bepilot.local", uuid.NewString()), "t")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)

	sess, err := st.Sessions.Create(ctx, CreateParams{UserID: user.ID, Title: "task result test"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.TaskResults.Upsert(ctx, sess.ID, "checklist", "Checklist hồ sơ",
		map[string]any{"items": []any{map[string]any{"label": "a", "status": "ok"}}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := st.TaskResults.Upsert(ctx, sess.ID, "extraction", "Thông tin trích xuất",
		map[string]any{"fields": []any{map[string]any{"label": "Bên vay", "value": "Công ty A"}}}); err != nil {
		t.Fatalf("second task upsert: %v", err)
	}

	// A second save for "checklist" must replace, not add a row.
	updated, err := st.TaskResults.Upsert(ctx, sess.ID, "checklist", "Checklist hồ sơ (v2)",
		map[string]any{"items": []any{map[string]any{"label": "a", "status": "missing", "note": "hostile\x00"}}})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if updated.Title != "Checklist hồ sơ (v2)" {
		t.Errorf("title = %q, want the replaced title", updated.Title)
	}

	rows, err := st.TaskResults.ListBySession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one per task_key): %+v", len(rows), rows)
	}

	byKey := map[string]bool{}
	for _, r := range rows {
		byKey[r.TaskKey] = true
		if r.SessionID != sess.ID {
			t.Errorf("row %s has session %s, want %s", r.TaskKey, r.SessionID, sess.ID)
		}
	}
	if !byKey["checklist"] || !byKey["extraction"] {
		t.Errorf("missing expected task_key rows: %+v", rows)
	}

	// A session with no saved results returns an empty (not nil-panicking) list.
	other, err := st.Sessions.Create(ctx, CreateParams{UserID: user.ID, Title: "no results"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.TaskResults.ListBySession(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListBySession (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("want 0 rows for a session with no results, got %d", len(empty))
	}
}
