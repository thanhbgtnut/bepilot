package dto

import (
	"time"

	"github.com/thanhenti/bepilot/internal/domain"
)

// CreateSessionRequest is the body of POST /v1/sessions.
type CreateSessionRequest struct {
	Title    string         `json:"title,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Model    string         `json:"model,omitempty"`
	System   string         `json:"system,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// UpdateSessionRequest is the body of PATCH /v1/sessions/{id}.
type UpdateSessionRequest struct {
	Title    *string        `json:"title,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SessionBrief is a session without its transcript.
type SessionBrief struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Provider  string         `json:"provider"`
	Model     string         `json:"model"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// SessionList is the paginated response of GET /v1/sessions.
type SessionList struct {
	Data       []SessionBrief `json:"data"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// SessionDetail includes the reconstructed transcript.
type SessionDetail struct {
	SessionBrief
	Messages []TranscriptMessage `json:"messages"`
}

// TranscriptMessage is one message in Anthropic content-array shape.
type TranscriptMessage struct {
	ID         string        `json:"id"`
	Seq        int           `json:"seq"`
	Role       string        `json:"role"`
	Content    []OutputBlock `json:"content"`
	StopReason string        `json:"stop_reason,omitempty"`
	Usage      *Usage        `json:"usage,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
}

// BriefFromDomain maps a domain session to its brief DTO.
func BriefFromDomain(s domain.Session) SessionBrief {
	return SessionBrief{
		ID:        s.ID.String(),
		Title:     s.Title,
		Provider:  s.Provider,
		Model:     s.Model,
		Summary:   s.Summary,
		Metadata:  s.Metadata,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// TranscriptFromMessages maps stored messages to transcript DTOs.
func TranscriptFromMessages(msgs []domain.Message) []TranscriptMessage {
	out := make([]TranscriptMessage, 0, len(msgs))
	for _, m := range msgs {
		tm := TranscriptMessage{
			ID:         m.ID.String(),
			Seq:        m.Seq,
			Role:       m.Role,
			Content:    BlocksToOutput(m.Blocks),
			StopReason: m.StopReason,
			CreatedAt:  m.CreatedAt,
		}
		if m.Role == domain.RoleAssistant {
			u := Usage{InputTokens: m.Usage.InputTokens, OutputTokens: m.Usage.OutputTokens}
			tm.Usage = &u
		}
		out = append(out, tm)
	}
	return out
}
