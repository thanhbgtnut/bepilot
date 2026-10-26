// Package api implements the HTTP handlers: an Anthropic-compatible
// /v1/messages endpoint (streaming and buffered) plus session and skill
// management endpoints.
package api

import (
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepilot/agentcore/agent"
	"github.com/thanhenti/bepilot/agentcore/llm"
	"github.com/thanhenti/bepilot/agentcore/mcp"
	"github.com/thanhenti/bepilot/agentcore/skills"
	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/document"
	"github.com/thanhenti/bepilot/internal/store"
)

// Handlers bundles the dependencies for every HTTP handler.
type Handlers struct {
	Store    *store.Store
	Agent    *agent.Agent
	Registry *llm.Registry
	Skills   *skills.Service
	MCP      *mcp.Manager // nil when MCP is disabled
	// Documents reads uploaded files for /v1/documents/*; nil when
	// documents.enabled is false (the endpoints answer 503).
	Documents *document.Service
	// Jobs wakes the background readers when a document is uploaded; nil
	// leaves them to their polling.
	Jobs     interface{ Notify() }
	Docs     config.Documents
	LLM      config.LLM
	Agentcfg config.AgentCfg
	Log      *slog.Logger
}

func (h *Handlers) badRequest(c *app.RequestContext, msg string) {
	c.JSON(consts.StatusBadRequest, dto.NewError("invalid_request_error", msg))
}

func (h *Handlers) notFound(c *app.RequestContext, msg string) {
	c.JSON(consts.StatusNotFound, dto.NewError("not_found_error", msg))
}

func (h *Handlers) serverError(c *app.RequestContext, err error) {
	h.Log.Error("handler error", "err", err, "path", string(c.Path()))
	c.JSON(consts.StatusInternalServerError, dto.NewError("api_error", err.Error()))
}
