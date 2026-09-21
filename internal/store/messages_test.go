package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/domain"
)

// TestAppendStoresHostileText needs a Postgres (TEST_DATABASE_URL). It writes
// only its own user and session, and removes them afterwards, so it is safe to
// point at a development database.
func TestAppendStoresHostileText(t *testing.T) {
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

	// A title cut mid-character, as the API used to produce.
	sess, err := st.Sessions.Create(ctx, CreateParams{UserID: user.ID, Title: "ạ"[:2] + "x\x00", Metadata: map[string]any{"k": "a\x00"}})
	if err != nil {
		t.Fatalf("session with hostile title: %v", err)
	}
	if sess.Title != "�x" {
		t.Errorf("title = %q", sess.Title)
	}

	msg := domain.Message{SessionID: sess.ID, Role: domain.RoleAssistant, Blocks: []domain.ContentBlock{
		{Type: domain.BlockText, Text: "Chào 5\xba tầng\x00"},
		{Type: domain.BlockToolUse, ToolName: "http_fetch", ToolUseID: "t1", ToolInput: map[string]any{"url": "x\x00y", "n": int64(9007199254740993)}},
		// The reported failure: a tool result with a byte that is not UTF-8.
		{Type: domain.BlockToolResult, ToolName: "http_fetch", ToolUseID: "t1", Text: "trang \xba cũ", ToolResult: map[string]any{"body": "\xba\x00"}},
	}}
	if err := st.Messages.Append(ctx, &msg); err != nil {
		t.Fatalf("Append must accept text the model or a tool produced: %v", err)
	}

	got, err := st.Messages.ListBySession(ctx, sess.ID, 0, 10)
	if err != nil || len(got) != 1 || len(got[0].Blocks) != 3 {
		t.Fatalf("read back: %v %+v", err, got)
	}
	b := got[0].Blocks
	if b[0].Text != "Chào 5� tầng" || b[2].Text != "trang � cũ" {
		t.Errorf("stored text: %q / %q", b[0].Text, b[2].Text)
	}
	if b[1].ToolInput["url"] != "xy" {
		t.Errorf("stored tool input: %v", b[1].ToolInput)
	}
	if n, _ := b[1].ToolInput["n"].(float64); n == 0 {
		t.Errorf("number lost: %v", b[1].ToolInput)
	}
}

// TestAppendConcurrentWritersGetDistinctSeq covers a message sent while a turn
// is still running: both writes target the same session at once. Before the
// per-session lock they read the same MAX(seq) and one failed or landed out of
// order.
func TestAppendConcurrentWritersGetDistinctSeq(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	user, err := st.Users.Create(ctx, fmt.Sprintf("store-test-%s@bepilot.local", uuid.NewString()), "t")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	sess, err := st.Sessions.Create(ctx, CreateParams{UserID: user.ID, Title: "concurrent"})
	if err != nil {
		t.Fatal(err)
	}

	const writers = 12
	seqs := make(chan int, writers)
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			m := domain.Message{SessionID: sess.ID, Role: domain.RoleUser,
				Blocks: []domain.ContentBlock{{Type: domain.BlockText, Text: fmt.Sprint("m", i)}}}
			if err := st.Messages.Append(ctx, &m); err != nil {
				errs <- err
				return
			}
			seqs <- m.Seq
		}(i)
	}
	seen := map[int]bool{}
	for i := 0; i < writers; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent Append failed: %v", err)
		case s := <-seqs:
			if seen[s] {
				t.Fatalf("seq %d handed out twice", s)
			}
			seen[s] = true
		}
	}
	for s := 1; s <= writers; s++ {
		if !seen[s] {
			t.Fatalf("seq %d missing: %v", s, seen)
		}
	}
}

// TestAgentRunDetailRoundTrips checks the migration and the insert for the
// per-run detail record, including text jsonb refuses.
func TestAgentRunDetailRoundTrips(t *testing.T) {
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
	sess, err := st.Sessions.Create(ctx, CreateParams{UserID: user.ID, Title: "detail"})
	if err != nil {
		t.Fatal(err)
	}

	err = st.Runs.Insert(ctx, domain.AgentRun{SessionID: sess.ID, Model: "m", Steps: 3, Detail: map[string]any{
		"stop_reason": "end_turn", "temperature": 0.2, "tool_calls": map[string]int{"calculate": 2}, "note": "a\x00b",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runs.Insert(ctx, domain.AgentRun{SessionID: sess.ID}); err != nil { // no detail at all
		t.Fatal(err)
	}
	var got string
	if err := st.Pool.QueryRow(ctx, `SELECT detail->'tool_calls'->>'calculate' FROM agent_runs WHERE session_id = $1 AND steps = 3`, sess.ID).Scan(&got); err != nil || got != "2" {
		t.Fatalf("detail not stored: %q %v", got, err)
	}
}
