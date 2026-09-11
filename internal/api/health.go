package api

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/thanhenti/bepilot/internal/api/dto"
)

// Healthz is a liveness probe.
//
// @Summary  Liveness probe
// @Tags     Health
// @Produce  json
// @Success  200  {object}  dto.HealthResponse
// @Router   /healthz [get]
func (h *Handlers) Healthz(_ context.Context, c *app.RequestContext) {
	c.JSON(consts.StatusOK, dto.HealthResponse{Status: "ok"})
}

// Readyz checks the database connection.
//
// @Summary  Readiness probe (checks the database)
// @Tags     Health
// @Produce  json
// @Success  200  {object}  dto.ReadyResponse
// @Failure  503  {object}  dto.ReadyResponse
// @Router   /readyz [get]
func (h *Handlers) Readyz(ctx context.Context, c *app.RequestContext) {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := h.Store.Pool.Ping(pingCtx); err != nil {
		c.JSON(consts.StatusServiceUnavailable, dto.ReadyResponse{Status: "unavailable", Error: err.Error()})
		return
	}
	c.JSON(consts.StatusOK, dto.ReadyResponse{
		Status:          "ready",
		DefaultProvider: h.LLM.DefaultProvider,
		DefaultModel:    h.LLM.DefaultModel,
		Providers:       h.Registry.Names(),
	})
}
