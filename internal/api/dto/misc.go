package dto

import "github.com/thanhenti/bepilot/internal/domain"

// MessageListResponse is the body of GET /v1/sessions/{id}/messages.
type MessageListResponse struct {
	Data []TranscriptMessage `json:"data"`
}

// DeleteResponse is the body returned by DELETE /v1/sessions/{id}.
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// SkillView is one skill as returned by GET /v1/skills.
type SkillView struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Enabled      bool     `json:"enabled"`
	HasEmbedding bool     `json:"has_embedding"`
}

// SkillListResponse is the body of GET /v1/skills.
type SkillListResponse struct {
	Data []SkillView `json:"data"`
}

// SkillSyncResult is the body of POST /v1/skills/sync.
type SkillSyncResult struct {
	Discovered int      `json:"discovered"`
	Embedded   int      `json:"embedded"`
	Deleted    int64    `json:"deleted"`
	Slugs      []string `json:"slugs"`
}

// HealthResponse is the body of GET /healthz.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// ReadyResponse is the body of GET /readyz.
type ReadyResponse struct {
	Status          string   `json:"status" example:"ready"`
	DefaultProvider string   `json:"default_provider,omitempty"`
	DefaultModel    string   `json:"default_model,omitempty"`
	Providers       []string `json:"providers,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// SkillViewFromDomain maps a domain skill to its API view.
func SkillViewFromDomain(s domain.Skill) SkillView {
	return SkillView{
		Slug:         s.Slug,
		Name:         s.Name,
		Description:  s.Description,
		AllowedTools: s.AllowedTools,
		Enabled:      s.Enabled,
		HasEmbedding: s.HasEmbedding,
	}
}
