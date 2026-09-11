package main

import (
	"context"
	"log/slog"

	"github.com/thanhenti/bepilot/internal/config"
	appmcp "github.com/thanhenti/bepilot/internal/mcp"
	"github.com/thanhenti/bepilot/internal/skills"
	"github.com/thanhenti/bepilot/internal/store"
	"github.com/thanhenti/bepilot/internal/tools"
)

func buildToolRegistry(skillSvc *skills.Service, cfg *config.Config) (*tools.Registry, error) {
	return tools.NewRegistry(skillSvc, cfg.Tools.HTTPAllowlist)
}

// bootstrapMCP attaches every MCP server known at startup: first the ones
// declared in the config file, then the ones added previously through the API
// (persisted in the database). Per-server failures are logged, not fatal.
func bootstrapMCP(ctx context.Context, m *appmcp.Manager, st *store.Store, cfg *config.Config, log *slog.Logger) {
	if cfg.MCP.File != "" {
		specs, err := config.LoadMCPServers(cfg.MCP.File)
		if err != nil {
			log.Warn("mcp: load config file failed", "path", cfg.MCP.File, "err", err)
		} else {
			m.AttachAll(ctx, specs, appmcp.SourceFile)
		}
	}

	rows, err := st.MCP.List(ctx)
	if err != nil {
		log.Warn("mcp: load persisted servers failed", "err", err)
		return
	}
	var specs []config.MCPServerSpec
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		s, err := config.MCPServerSpecFromMap(row.Spec)
		if err != nil {
			log.Warn("mcp: skipping malformed persisted server", "name", row.Name, "err", err)
			continue
		}
		specs = append(specs, s)
	}
	m.AttachAll(ctx, specs, appmcp.SourceAPI)
}
