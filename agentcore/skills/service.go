package skills

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/agentcore/domain"
	"github.com/thanhenti/bepilot/agentcore/retrieval"
)

// Repo is where skills and their embeddings are indexed. memstore.Skills is
// an in-memory one.
type Repo interface {
	// Upsert inserts or updates a skill by slug, reporting whether its body
	// (checksum) changed.
	Upsert(ctx context.Context, s domain.Skill) (id uuid.UUID, changed bool, err error)
	SetEmbedding(ctx context.Context, id uuid.UUID, vec []float32) error
	// DeleteMissing removes every skill whose slug is not in keep.
	DeleteMissing(ctx context.Context, keep []string) (int64, error)
	List(ctx context.Context) ([]domain.Skill, error)
	GetBySlug(ctx context.Context, slug string) (domain.Skill, error)
	// Retrieve returns the top-k enabled, embedded skills nearest to vec by
	// cosine similarity, nearest first.
	Retrieve(ctx context.Context, vec []float32, k int) ([]domain.SkillMatch, error)
}

// Service is the application-facing API for skills.
type Service struct {
	repo     Repo
	embedder retrieval.Embedder
	dir      string
	log      *slog.Logger
}

// NewService wires the skill service. dir holds the SKILL.md tree that Sync
// reads; log may be nil.
func NewService(repo Repo, embedder retrieval.Embedder, dir string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{repo: repo, embedder: embedder, dir: dir, log: log}
}

// SyncResult summarises a sync run.
type SyncResult struct {
	Discovered int      `json:"discovered"`
	Embedded   int      `json:"embedded"`
	Deleted    int64    `json:"deleted"`
	Slugs      []string `json:"slugs"`
}

// Sync scans the skills directory and reconciles the database: upserts every
// skill, (re)embeds those whose body changed or that lack an embedding, and
// deletes rows whose source directory is gone.
func (s *Service) Sync(ctx context.Context) (SyncResult, error) {
	discovered, err := Discover(s.dir)
	if err != nil {
		return SyncResult{}, err
	}

	res := SyncResult{Discovered: len(discovered), Slugs: slugList(discovered)}
	existing, err := s.repo.List(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	hasEmbedding := map[string]bool{}
	for _, e := range existing {
		hasEmbedding[e.Slug] = e.HasEmbedding
	}

	for _, d := range discovered {
		id, changed, err := s.repo.Upsert(ctx, domain.Skill{
			Slug:         d.Slug,
			Name:         d.Name,
			Description:  d.Description,
			Path:         d.Dir,
			Body:         d.Body,
			Checksum:     d.Checksum,
			AllowedTools: d.AllowedTools,
			Enabled:      true,
		})
		if err != nil {
			return SyncResult{}, err
		}
		if changed || !hasEmbedding[d.Slug] {
			vecs, err := s.embedder.Embed(ctx, []string{d.embedText()})
			if err != nil {
				return SyncResult{}, fmt.Errorf("embed skill %q: %w", d.Slug, err)
			}
			if err := s.repo.SetEmbedding(ctx, id, vecs[0]); err != nil {
				return SyncResult{}, err
			}
			res.Embedded++
		}
	}

	deleted, err := s.repo.DeleteMissing(ctx, res.Slugs)
	if err != nil {
		return SyncResult{}, err
	}
	res.Deleted = deleted
	s.log.Info("skills synced", "discovered", res.Discovered, "embedded", res.Embedded, "deleted", res.Deleted)
	return res, nil
}

// List returns all skills.
func (s *Service) List(ctx context.Context) ([]domain.Skill, error) {
	return s.repo.List(ctx)
}

// Retrieve returns the skills most relevant to queryText, ordered by descending
// similarity. It never errors fatally: on embedder failure it returns nil so the
// agent degrades to "no skills suggested" rather than failing the turn.
func (s *Service) Retrieve(ctx context.Context, queryText string, k int) []domain.SkillMatch {
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return nil
	}
	vecs, err := s.embedder.Embed(ctx, []string{queryText})
	if err != nil {
		s.log.Warn("skill retrieval: embed failed", "err", err)
		return nil
	}
	matches, err := s.repo.Retrieve(ctx, vecs[0], k)
	if err != nil {
		s.log.Warn("skill retrieval: query failed", "err", err)
		return nil
	}
	return matches
}

// Loaded is the payload returned by the load_skill tool.
type Loaded struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Body         string   `json:"body"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Resources    []string `json:"resources,omitempty"`
}

// Load returns the full skill document plus a listing of its bundled resource
// files (relative paths), so the model can decide what else to read.
func (s *Service) Load(ctx context.Context, slug string) (Loaded, error) {
	sk, err := s.repo.GetBySlug(ctx, slug)
	if err != nil {
		return Loaded{}, err
	}
	res, _ := listResources(sk.Path)
	return Loaded{
		Slug:         sk.Slug,
		Name:         sk.Name,
		Description:  sk.Description,
		Body:         sk.Body,
		AllowedTools: sk.AllowedTools,
		Resources:    res,
	}, nil
}
