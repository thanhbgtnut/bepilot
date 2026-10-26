// Package memstore keeps conversations and skills in memory. Store satisfies
// agent.Store and Skills satisfies skills.Repo, so the agent runs with no
// database: nothing survives the process.
package memstore

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/agentcore/domain"
)

// ErrNotFound is returned for an unknown session or skill.
var ErrNotFound = errors.New("memstore: not found")

// Store holds sessions, their messages and the agent's run records.
type Store struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*domain.Session
	messages map[uuid.UUID][]domain.Message
	runs     map[uuid.UUID][]domain.AgentRun
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		sessions: map[uuid.UUID]*domain.Session{},
		messages: map[uuid.UUID][]domain.Message{},
		runs:     map[uuid.UUID][]domain.AgentRun{},
	}
}

// CreateSession stores s under a new ID and returns it.
func (s *Store) CreateSession(sess domain.Session) domain.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess.ID = uuid.New()
	sess.CreatedAt, sess.UpdatedAt = now, now
	s.sessions[sess.ID] = &sess
	return sess
}

// Session returns the session with the given ID, including its current
// summary.
func (s *Store) Session(id uuid.UUID) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return domain.Session{}, ErrNotFound
	}
	return *sess, nil
}

// Runs returns the run records of a session, oldest first.
func (s *Store) Runs(sessionID uuid.UUID) []domain.AgentRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.AgentRun(nil), s.runs[sessionID]...)
}

// AppendMessage implements agent.Store.
func (s *Store) AppendMessage(_ context.Context, m *domain.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[m.SessionID]; !ok {
		return ErrNotFound
	}
	m.ID = uuid.New()
	m.Seq = len(s.messages[m.SessionID]) + 1
	m.CreatedAt = time.Now()
	for i := range m.Blocks {
		m.Blocks[i].ID = uuid.New()
		m.Blocks[i].MessageID = m.ID
		m.Blocks[i].Idx = i
	}
	s.messages[m.SessionID] = append(s.messages[m.SessionID], copyMessage(*m))
	return nil
}

// ListMessages implements agent.Store.
func (s *Store) ListMessages(_ context.Context, sessionID uuid.UUID, limit int) ([]domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.messages[sessionID]
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[:limit]
	}
	out := make([]domain.Message, len(msgs))
	for i, m := range msgs {
		out[i] = copyMessage(m)
	}
	return out, nil
}

// TouchSession implements agent.Store.
func (s *Store) TouchSession(_ context.Context, sessionID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	sess.UpdatedAt = time.Now()
	return nil
}

// SetSessionSummary implements agent.Store.
func (s *Store) SetSessionSummary(_ context.Context, sessionID uuid.UUID, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	sess.Summary = summary
	sess.UpdatedAt = time.Now()
	return nil
}

// RecordRun implements agent.Store.
func (s *Store) RecordRun(_ context.Context, run domain.AgentRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run.ID = uuid.New()
	run.CreatedAt = time.Now()
	s.runs[run.SessionID] = append(s.runs[run.SessionID], run)
	return nil
}

func copyMessage(m domain.Message) domain.Message {
	m.Blocks = append([]domain.ContentBlock(nil), m.Blocks...)
	return m
}

// Skills holds skills and their embeddings.
type Skills struct {
	mu     sync.Mutex
	bySlug map[string]*skillRow
}

type skillRow struct {
	skill domain.Skill
	vec   []float32
}

// NewSkills returns an empty Skills.
func NewSkills() *Skills { return &Skills{bySlug: map[string]*skillRow{}} }

// Upsert implements skills.Repo.
func (r *Skills) Upsert(_ context.Context, s domain.Skill) (uuid.UUID, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.bySlug[s.Slug]
	if !ok {
		s.ID = uuid.New()
		s.HasEmbedding = false
		s.UpdatedAt = time.Now()
		r.bySlug[s.Slug] = &skillRow{skill: s}
		return s.ID, true, nil
	}
	changed := row.skill.Checksum != s.Checksum
	s.ID = row.skill.ID
	s.HasEmbedding = row.vec != nil
	s.UpdatedAt = time.Now()
	row.skill = s
	return s.ID, changed, nil
}

// SetEmbedding implements skills.Repo.
func (r *Skills) SetEmbedding(_ context.Context, id uuid.UUID, vec []float32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.bySlug {
		if row.skill.ID == id {
			row.vec = append([]float32(nil), vec...)
			row.skill.HasEmbedding = true
			return nil
		}
	}
	return ErrNotFound
}

// DeleteMissing implements skills.Repo.
func (r *Skills) DeleteMissing(_ context.Context, keep []string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make(map[string]bool, len(keep))
	for _, k := range keep {
		kept[k] = true
	}
	var n int64
	for slug := range r.bySlug {
		if !kept[slug] {
			delete(r.bySlug, slug)
			n++
		}
	}
	return n, nil
}

// List implements skills.Repo.
func (r *Skills) List(_ context.Context) ([]domain.Skill, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Skill, 0, len(r.bySlug))
	for _, row := range r.bySlug {
		out = append(out, row.skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// GetBySlug implements skills.Repo.
func (r *Skills) GetBySlug(_ context.Context, slug string) (domain.Skill, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.bySlug[slug]
	if !ok {
		return domain.Skill{}, ErrNotFound
	}
	return row.skill, nil
}

// Retrieve implements skills.Repo.
func (r *Skills) Retrieve(_ context.Context, vec []float32, k int) ([]domain.SkillMatch, error) {
	if k <= 0 {
		k = 4
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.SkillMatch
	for _, row := range r.bySlug {
		if !row.skill.Enabled || row.vec == nil {
			continue
		}
		out = append(out, domain.SkillMatch{Skill: row.skill, Score: cosine(vec, row.vec)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
