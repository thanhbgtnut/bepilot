package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// SkillsRepo persists skills and performs vector similarity retrieval.
type SkillsRepo struct{ pool *pgxpool.Pool }

// Upsert inserts or updates a skill by slug. It leaves the embedding untouched
// (SetEmbedding writes that separately) and returns whether the body changed.
func (r *SkillsRepo) Upsert(ctx context.Context, s domain.Skill) (id uuid.UUID, changed bool, err error) {
	var existingChecksum string
	err = r.pool.QueryRow(ctx, `SELECT id, checksum FROM skills WHERE slug = $1`, s.Slug).Scan(&id, &existingChecksum)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		err = r.pool.QueryRow(ctx, `
			INSERT INTO skills (slug, name, description, path, body, checksum, allowed_tools, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`,
			s.Slug, s.Name, s.Description, s.Path, s.Body, s.Checksum, s.AllowedTools, s.Enabled,
		).Scan(&id)
		return id, true, err
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("skills.Upsert: %w", err)
	}

	changed = existingChecksum != s.Checksum
	_, err = r.pool.Exec(ctx, `
		UPDATE skills SET name=$2, description=$3, path=$4, body=$5, checksum=$6,
			allowed_tools=$7, enabled=$8, updated_at=now()
		WHERE id=$1`,
		id, s.Name, s.Description, s.Path, s.Body, s.Checksum, s.AllowedTools, s.Enabled)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("skills.Upsert update: %w", err)
	}
	return id, changed, nil
}

// SetEmbedding stores the embedding vector for a skill.
func (r *SkillsRepo) SetEmbedding(ctx context.Context, id uuid.UUID, vec []float32) error {
	_, err := r.pool.Exec(ctx, `UPDATE skills SET embedding = $2::vector WHERE id = $1`, id, encodeVector(vec))
	if err != nil {
		return fmt.Errorf("skills.SetEmbedding: %w", err)
	}
	return nil
}

// DeleteMissing removes skills whose slug is not in keep (skills deleted from
// the source directory).
func (r *SkillsRepo) DeleteMissing(ctx context.Context, keep []string) (int64, error) {
	ct, err := r.pool.Exec(ctx, `DELETE FROM skills WHERE slug <> ALL($1)`, keep)
	if err != nil {
		return 0, fmt.Errorf("skills.DeleteMissing: %w", err)
	}
	return ct.RowsAffected(), nil
}

// List returns all skills, ordered by name.
func (r *SkillsRepo) List(ctx context.Context) ([]domain.Skill, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, name, description, path, body, checksum, allowed_tools, enabled,
			(embedding IS NOT NULL) AS has_embedding, updated_at
		FROM skills ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("skills.List: %w", err)
	}
	defer rows.Close()
	return scanSkills(rows)
}

// GetBySlug returns one enabled skill.
func (r *SkillsRepo) GetBySlug(ctx context.Context, slug string) (domain.Skill, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, name, description, path, body, checksum, allowed_tools, enabled,
			(embedding IS NOT NULL) AS has_embedding, updated_at
		FROM skills WHERE slug = $1`, slug)
	if err != nil {
		return domain.Skill{}, fmt.Errorf("skills.GetBySlug: %w", err)
	}
	defer rows.Close()
	list, err := scanSkills(rows)
	if err != nil {
		return domain.Skill{}, err
	}
	if len(list) == 0 {
		return domain.Skill{}, ErrNotFound
	}
	return list[0], nil
}

// SkillMatch is a retrieval hit with its cosine similarity in [0,1].
type SkillMatch struct {
	domain.Skill
	Score float64
}

// Retrieve returns the top-k enabled skills nearest to the query vector by
// cosine similarity. Skills without an embedding are excluded.
func (r *SkillsRepo) Retrieve(ctx context.Context, vec []float32, k int) ([]SkillMatch, error) {
	if k <= 0 {
		k = 4
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, name, description, path, body, checksum, allowed_tools, enabled,
			(embedding IS NOT NULL) AS has_embedding, updated_at,
			1 - (embedding <=> $1::vector) AS score
		FROM skills
		WHERE enabled AND embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`, encodeVector(vec), k)
	if err != nil {
		return nil, fmt.Errorf("skills.Retrieve: %w", err)
	}
	defer rows.Close()

	var out []SkillMatch
	for rows.Next() {
		var s domain.Skill
		var m SkillMatch
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.Path, &s.Body, &s.Checksum,
			&s.AllowedTools, &s.Enabled, &s.HasEmbedding, &s.UpdatedAt, &m.Score); err != nil {
			return nil, err
		}
		m.Skill = s
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanSkills(rows pgx.Rows) ([]domain.Skill, error) {
	var out []domain.Skill
	for rows.Next() {
		var s domain.Skill
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.Path, &s.Body, &s.Checksum,
			&s.AllowedTools, &s.Enabled, &s.HasEmbedding, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// encodeVector renders a float slice as a pgvector literal: "[1,2,3]".
func encodeVector(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
