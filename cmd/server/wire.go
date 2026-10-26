package main

import (
	"context"
	"log/slog"

	appmcp "github.com/thanhenti/bepilot/agentcore/mcp"
	"github.com/thanhenti/bepilot/agentcore/skills"
	"github.com/thanhenti/bepilot/agentcore/tools"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/store"
)

func buildToolRegistry(skillSvc *skills.Service, cfg *config.Config, st *store.Store) (*tools.Registry, error) {
	return tools.NewRegistry(skillSvc, cfg.Tools.HTTPAllowlist, st.TaskResults)
}

// bootstrapMCP attaches every MCP server known at startup: first the ones
// declared in the config file, then the ones added previously through the API
// (persisted in the database). Per-server failures are logged, not fatal.
func bootstrapMCP(ctx context.Context, m *appmcp.Manager, st *store.Store, cfg *config.Config, log *slog.Logger) {
	if cfg.MCP.File != "" {
		specs, err := appmcp.LoadServers(cfg.MCP.File)
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
	var specs []appmcp.ServerSpec
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		s, err := appmcp.ServerSpecFromMap(row.Spec)
		if err != nil {
			log.Warn("mcp: skipping malformed persisted server", "name", row.Name, "err", err)
			continue
		}
		specs = append(specs, s)
	}
	m.AttachAll(ctx, specs, appmcp.SourceAPI)
}
