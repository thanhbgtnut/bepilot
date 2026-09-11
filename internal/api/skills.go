package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepilot/internal/api/dto"
)

// SyncSkills handles POST /v1/skills/sync — rescans the skills directory and
// reconciles the database (upsert, re-embed changed, delete removed).
//
// @Summary   Rescan the skills directory and reconcile the database
// @Tags      Skills
// @Produce   json
// @Success   200  {object}  dto.SkillSyncResult
// @Failure   401  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/skills/sync [post]
func (h *Handlers) SyncSkills(ctx context.Context, c *app.RequestContext) {
	res, err := h.Skills.Sync(ctx)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.SkillSyncResult{
		Discovered: res.Discovered,
		Embedded:   res.Embedded,
		Deleted:    res.Deleted,
		Slugs:      res.Slugs,
	})
}

// ListSkills handles GET /v1/skills.
//
// @Summary   List skills and their status
// @Tags      Skills
// @Produce   json
// @Success   200  {object}  dto.SkillListResponse
// @Failure   401  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/skills [get]
func (h *Handlers) ListSkills(ctx context.Context, c *app.RequestContext) {
	list, err := h.Skills.List(ctx)
	if err != nil {
		h.serverError(c, err)
		return
	}
	out := make([]dto.SkillView, 0, len(list))
	for _, s := range list {
		out = append(out, dto.SkillViewFromDomain(s))
	}
	c.JSON(consts.StatusOK, dto.SkillListResponse{Data: out})
}
