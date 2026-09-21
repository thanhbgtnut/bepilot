package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTrimHistoryKeepsOversizedLatestUser(t *testing.T) {
	msgs := []*schema.Message{{Role: schema.User, Content: strings.Repeat("a", 24000*4+100)}}
	got := trimHistory(msgs, 24000)
	if len(got) != 1 || got[0].Role != schema.User {
		t.Fatalf("want the single user message kept, got %d messages", len(got))
	}
}

func TestTrimHistoryStartsAtUser(t *testing.T) {
	big := strings.Repeat("a", 400)
	msgs := []*schema.Message{
		{Role: schema.User, Content: big},
		{Role: schema.Assistant, Content: big},
		{Role: schema.Tool, Content: big},
		{Role: schema.Assistant, Content: "short"},
		{Role: schema.User, Content: "latest"},
	}
	got := trimHistory(msgs, 150)
	if len(got) == 0 || got[0].Role != schema.User {
		t.Fatalf("history must begin with a user message, got %+v", got)
	}
	if got[len(got)-1].Content != "latest" {
		t.Fatal("latest user message must be kept")
	}
}
