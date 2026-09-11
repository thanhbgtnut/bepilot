// Package server wires the Hertz HTTP server: middleware stack and routes.
package server

import (
	"context"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	hertzSwagger "github.com/hertz-contrib/swagger"
	swaggerFiles "github.com/swaggo/files"

	"github.com/thanhenti/bepilot/internal/api"
	appcfg "github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/server/middleware"
)

// New builds the Hertz engine with all routes mounted.
func New(cfg appcfg.HTTP, h *api.Handlers, log *slog.Logger) *server.Hertz {
	opts := []config.Option{
		server.WithHostPorts(cfg.Addr),
		server.WithDisablePrintRoute(true),
		server.WithReadTimeout(cfg.ReadTimeout),
		server.WithWriteTimeout(cfg.WriteTimeout),
		server.WithExitWaitTime(cfg.ShutdownTimeout),
		server.WithStreamBody(true),
		server.WithSenseClientDisconnection(true),
	}
	hz := server.New(opts...)

	hz.Use(
		middleware.RequestID(),
		middleware.Recovery(log),
		middleware.AccessLog(log),
		middleware.CORS(cfg.CORSOrigins),
	)

	hz.GET("/healthz", h.Healthz)
	hz.GET("/readyz", h.Readyz)

	// API documentation (no auth). Swagger UI at /swagger/index.html; the raw
	// generated spec at /openapi.yaml; /docs redirects to the UI.
	hz.GET("/swagger/*any", hertzSwagger.WrapHandler(swaggerFiles.Handler))
	hz.GET("/openapi.yaml", h.OpenAPISpec)
	hz.GET("/docs", h.DocsRedirect)

	v1 := hz.Group("/v1", middleware.Auth(h.Store.APIKeys))
	{
		v1.POST("/messages", h.Messages)

		// AG-UI protocol surface for client SDKs (CopilotKit et al.).
		agui := v1.Group("/ag-ui")
		agui.POST("/run", h.AGUIRunAgent)

		v1.POST("/sessions", h.CreateSession)
		v1.GET("/sessions", h.ListSessions)
		v1.GET("/sessions/:id", h.GetSession)
		v1.GET("/sessions/:id/messages", h.ListSessionMessages)
		v1.PATCH("/sessions/:id", h.UpdateSession)
		v1.DELETE("/sessions/:id", h.DeleteSession)

		v1.POST("/skills/sync", h.SyncSkills)
		v1.GET("/skills", h.ListSkills)

		v1.GET("/mcp/servers", h.ListMCPServers)
		v1.POST("/mcp/servers", h.AddMCPServer)
		v1.DELETE("/mcp/servers/:name", h.DeleteMCPServer)
	}

	return hz
}

// Run starts the server and blocks until ctx is cancelled, then shuts down
// gracefully.
func Run(ctx context.Context, hz *server.Hertz) {
	go func() {
		<-ctx.Done()
		_ = hz.Shutdown(context.Background())
	}()
	hz.Spin()
}
