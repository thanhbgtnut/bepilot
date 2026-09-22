package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/thanhenti/bepilot/internal/tools"
)

// cutModel writes a long answer in pieces of at most `piece` runes, reporting
// finish_reason "length" on every piece but the last — what a provider does when
// max_tokens is smaller than the answer.
type cutModel struct {
	mu     *sync.Mutex
	answer string
	piece  int
	seen   *[][]*schema.Message
	fail   bool
}

func (m *cutModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

func (m *cutModel) next(in []*schema.Message) *schema.Message {
	m.mu.Lock()
	*m.seen = append(*m.seen, in)
	m.mu.Unlock()
	// What has been written so far is the assistant message the resume appended.
	done := ""
	if n := len(in); n >= 2 && in[n-1].Content == continueNotice {
		done = in[n-2].Content
	}
	rest := strings.TrimPrefix(m.answer, done)
	finish := "stop"
	if len([]rune(rest)) > m.piece {
		rest = string([]rune(rest)[:m.piece])
		finish = "length"
	}
	return &schema.Message{Role: schema.Assistant, Content: rest, ResponseMeta: &schema.ResponseMeta{FinishReason: finish}}
}

func (m *cutModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.next(in), nil
}

func (m *cutModel) Stream(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg := m.next(in)
	// Deliver each piece as several chunks, the finish reason on the last one.
	rs := []rune(msg.Content)
	var chunks []*schema.Message
	for i := 0; i < len(rs); i += 5 {
		j := min(i+5, len(rs))
		c := &schema.Message{Role: schema.Assistant, Content: string(rs[i:j])}
		if j == len(rs) {
			c.ResponseMeta = msg.ResponseMeta
		}
		chunks = append(chunks, c)
	}
	return schema.StreamReaderFromArray(chunks), nil
}

func TestTruncatedAnswerIsContinuedUntilComplete(t *testing.T) {
	answer := strings.Repeat("0123456789", 12) // 120 runes, model writes 25 at a time
	for _, mode := range []string{"stream", "generate"} {
		var (
			mu   sync.Mutex
			seen [][]*schema.Message
		)
		cm := &cutModel{mu: &mu, answer: answer, piece: 25, seen: &seen}
		var got string
		switch mode {
		case "stream":
			sr, err := streamResuming(context.Background(), cm, []*schema.Message{schema.UserMessage("write it")})
			if err != nil {
				t.Fatal(err)
			}
			for {
				c, err := sr.Recv()
				if err != nil {
					break
				}
				got += c.Content
			}
		default:
			msg, err := generateResuming(context.Background(), cm, []*schema.Message{schema.UserMessage("write it")})
			if err != nil {
				t.Fatal(err)
			}
			got = msg.Content
		}
		if got != answer {
			t.Errorf("%s: got %d runes %q, want the whole answer", mode, len([]rune(got)), got)
		}
		if len(seen) != 5 {
			t.Errorf("%s: model called %d times, want 5 (4 cut-offs + the rest)", mode, len(seen))
		}
		last := seen[len(seen)-1]
		if last[len(last)-1].Content != continueNotice || last[len(last)-2].Role != schema.Assistant {
			t.Errorf("%s: a continuation must end with the partial reply and the notice", mode)
		}
	}
}

func TestContinuationIsBoundedAndSkipsToolCalls(t *testing.T) {
	var (
		mu   sync.Mutex
		seen [][]*schema.Message
	)
	// Never finishes: must stop after maxContinuations extra calls.
	cm := &cutModel{mu: &mu, answer: strings.Repeat("x", 10_000), piece: 3, seen: &seen}
	msg, err := generateResuming(context.Background(), cm, []*schema.Message{schema.UserMessage("go")})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1+maxContinuations {
		t.Fatalf("calls = %d, want %d", len(seen), 1+maxContinuations)
	}
	if len(msg.Content) != 3*(1+maxContinuations) {
		t.Fatalf("content length %d", len(msg.Content))
	}

	if hitOutputLimit("length", true) {
		t.Fatal("a cut-off tool call must not be 'continued'")
	}
	if !hitOutputLimit("max_tokens", false) || !hitOutputLimit("LENGTH", false) || hitOutputLimit("stop", false) || hitOutputLimit("", false) {
		t.Fatal("finish reason mapping is wrong")
	}
}

func TestContinuationWorksInsideTheReactLoop(t *testing.T) {
	reg, err := tools.NewRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := reg.NewSession()
	ctx := tools.WithSession(context.Background(), sess)
	answer := strings.Repeat("abcdefghij", 8)
	var (
		mu   sync.Mutex
		seen [][]*schema.Message
	)
	ra, err := buildReactAgent(ctx, sess.WrapModel(&cutModel{mu: &mu, answer: answer, piece: 30, seen: &seen}), sess.Executable(), "sys", 16, nil)
	if err != nil {
		t.Fatal(err)
	}
	asm := newAssembler(nil)
	if err := (&Agent{}).drive(ctx, ra, []*einoMessage{{Role: schema.User, Content: "write"}}, testCallbackHandler(asm)); err != nil {
		t.Fatal(err)
	}
	blocks, _ := asm.result("end_turn")
	var got string
	for _, b := range blocks {
		if b.Type == "text" {
			got += b.Text
		}
	}
	if got != answer {
		t.Fatalf("got %q", got)
	}
}
