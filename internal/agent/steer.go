package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/domain"
)

// steerNotice frames a message that arrived while the turn was already
// running, so the model treats it as a change of plan rather than as a fresh
// conversation turn it has to start over from.
const steerNotice = "[New message from the user, sent while you were still working on the previous request. Take it into account from now on: if it changes or narrows the task, adapt and carry on; if it is a question, answer it briefly in your next reply and then continue the task. Do not start over.]\n"

// steerMsg is a user message accepted into a running turn. Msg is the already
// persisted row, kept so the message can be answered by a follow-up turn if
// the run ends before the model gets to read it.
type steerMsg struct {
	Text string
	Msg  domain.Message
}

// steerer is the inbox of one running turn. push is called from other requests
// of the same session; apply is called by the turn itself before every model
// call and folds whatever arrived into the transcript.
type steerer struct {
	mu      sync.Mutex
	closed  bool
	pending []steerMsg
	placed  []placedMsg
	taken   int // messages ever accepted
}

// placedMsg is a delivered message together with where in the transcript the
// model first saw it. The ReAct state only ever grows at the end, so replaying
// the message at the same index on later calls keeps it in chronological order
// (after the tool results it followed, before the steps that reacted to it).
type placedMsg struct {
	at  int
	msg *schema.Message
}

// push queues a message for the running turn. It reports false once the turn
// has stopped reading its inbox; the caller must then run the message as a turn
// of its own.
func (s *steerer) push(m steerMsg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.pending = append(s.pending, m)
	s.taken++
	return true
}

// accepted is how many messages the turn took in while running.
func (s *steerer) accepted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taken
}

// close stops accepting messages and returns those the model never got to see.
func (s *steerer) close() []steerMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	left := s.pending
	s.pending = nil
	return left
}

// apply returns the transcript for the next model call with every message
// delivered so far spliced in. It never mutates in.
func (s *steerer) apply(in []*schema.Message) []*schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(in)
	for _, p := range s.pending {
		s.placed = append(s.placed, placedMsg{at: n, msg: schema.UserMessage(steerNotice + p.Text)})
	}
	s.pending = nil
	if len(s.placed) == 0 {
		return in
	}
	out := make([]*schema.Message, 0, n+len(s.placed))
	j := 0
	for i := 0; i <= n; i++ {
		for j < len(s.placed) && s.placed[j].at <= i {
			out = append(out, s.placed[j].msg)
			j++
		}
		if i < n {
			out = append(out, in[i])
		}
	}
	return out
}

// activeRun is the turn currently executing for a session.
type activeRun struct {
	steer       *steerer
	cancel      context.CancelFunc
	interrupted atomic.Bool
	done        chan struct{} // closed once the turn is fully persisted and released

	// left holds messages accepted by the turn that its model never read. Only
	// the goroutine running the turn touches it.
	left []steerMsg
}

// interrupt cancels the turn's context. The turn notices, stores whatever it
// produced so far, and ends cleanly with stop reason "interrupted".
func (r *activeRun) interrupt() {
	r.interrupted.Store(true)
	r.cancel()
}

// runRegistry tracks at most one running turn per session. Two turns on the
// same session used to run side by side: each loaded a history that lacked the
// other's messages, and both wrote an assistant reply.
type runRegistry struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*activeRun
}

func newRunRegistry() *runRegistry { return &runRegistry{runs: map[uuid.UUID]*activeRun{}} }

// acquire registers a new turn for the session, or returns the one already
// running (owner == false).
func (g *runRegistry) acquire(id uuid.UUID, cancel context.CancelFunc) (r *activeRun, owner bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cur, ok := g.runs[id]; ok {
		return cur, false
	}
	r = &activeRun{steer: &steerer{}, cancel: cancel, done: make(chan struct{})}
	g.runs[id] = r
	return r, true
}

// release removes a finished turn and wakes everything waiting on it.
func (g *runRegistry) release(id uuid.UUID, r *activeRun) {
	g.mu.Lock()
	if g.runs[id] == r {
		delete(g.runs, id)
	}
	g.mu.Unlock()
	close(r.done)
}

// stopWords are the first words of a message that asks the agent to stop what
// it is doing. Only short messages count, so a longer instruction that merely
// starts with one of them ("hủy hợp đồng số 3 giúp tôi") is not mistaken for a
// cancel.
var stopWords = map[string]struct{}{
	"stop": {}, "cancel": {}, "abort": {}, "halt": {},
	"dừng": {}, "dung": {}, "hủy": {}, "huỷ": {}, "huy": {},
}

const maxStopWords = 3

// isStopRequest reports whether text is a request to abandon the running turn.
func isStopRequest(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) == 0 || len(words) > maxStopWords {
		return false
	}
	_, ok := stopWords[words[0]]
	return ok
}
