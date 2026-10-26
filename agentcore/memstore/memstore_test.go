package memstore_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/agentcore/agent"
	"github.com/thanhenti/bepilot/agentcore/agent/events"
	"github.com/thanhenti/bepilot/agentcore/domain"
	"github.com/thanhenti/bepilot/agentcore/llm"
	"github.com/thanhenti/bepilot/agentcore/llm/fakeprovider"
	"github.com/thanhenti/bepilot/agentcore/memstore"
	"github.com/thanhenti/bepilot/agentcore/retrieval"
	"github.com/thanhenti/bepilot/agentcore/skills"
	"github.com/thanhenti/bepilot/agentcore/tools"
)

// TestAgentRunsWithoutDatabase runs whole turns on the in-memory store with the
// fake provider: no network, no database.
func TestAgentRunsWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()

	skillSvc := skills.NewService(memstore.NewSkills(), retrieval.NewHashEmbedder(64), t.TempDir(), nil)
	toolReg, err := tools.NewRegistry(skillSvc, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := llm.NewRegistry(llm.Config{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	reg.Register("fake", fakeprovider.New(""))

	ag := agent.New(st, reg, toolReg, skillSvc, agent.Config{}, llm.Config{}, nil)
	sess := st.CreateSession(domain.Session{Title: "test"})

	for _, text := range []string{"hello", "and again"} {
		var kinds []events.Kind
		out, err := ag.Run(ctx, agent.RunInput{Session: sess, UserText: text, Model: "fake-1"}, func(e events.Event) { kinds = append(kinds, e.Kind) })
		if err != nil {
			t.Fatal(err)
		}
		if out.AssistantMessage.ID == uuid.Nil {
			t.Fatal("assistant message was not stored")
		}
		if len(kinds) == 0 || kinds[0] != events.KindMessageStart {
			t.Fatalf("events = %v, want message_start first", kinds)
		}
	}

	msgs, err := st.ListMessages(ctx, sess.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("stored %d messages, want 4", len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != i+1 {
			t.Fatalf("message %d has seq %d", i, m.Seq)
		}
	}
	if runs := st.Runs(sess.ID); len(runs) != 2 {
		t.Fatalf("recorded %d runs, want 2", len(runs))
	}
}
