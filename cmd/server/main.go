// Command server runs the bepilot HTTP API.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/thanhenti/bepilot/docs" // generated OpenAPI docs (make swag)
	"github.com/thanhenti/bepilot/internal/agent"
	"github.com/thanhenti/bepilot/internal/api"
	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/llm"
	"github.com/thanhenti/bepilot/internal/llm/fakeprovider"
	"github.com/thanhenti/bepilot/internal/logging"
	appmcp "github.com/thanhenti/bepilot/internal/mcp"
	"github.com/thanhenti/bepilot/internal/retrieval"
	"github.com/thanhenti/bepilot/internal/server"
	"github.com/thanhenti/bepilot/internal/skills"
	"github.com/thanhenti/bepilot/internal/store"
)

// @title                       bepilot API
// @version                     1.0.0
// @description                 A streaming AI agent backend. `/v1/messages` mirrors the Anthropic Messages API (with additive `provider` and `metadata.session_id` fields); the `/v1/sessions` endpoints manage conversations and let you list a user's chats and replay transcripts.
// @BasePath                    /
// @schemes                     http https
// @securityDefinitions.apikey  ApiKeyAuth
// @in                          header
// @name                        x-api-key
// @description                 An API key issued by `cmd/seed`. `Authorization: Bearer <key>` is also accepted.

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	migrateOnly := flag.Bool("migrate-only", false, "run migrations then exit")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.Log)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *migrateOnly {
		if err := store.Migrate(cfg.DB.DSN); err != nil {
			log.Error("migration failed", "err", err)
			os.Exit(1)
		}
		log.Info("migrations applied")
		return
	}

	st, err := store.Open(ctx, cfg.DB)
	if err != nil {
		log.Error("database init failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	embedder, err := retrieval.New(cfg.Embd)
	if err != nil {
		log.Error("embedder init failed", "err", err)
		os.Exit(1)
	}
	log.Info("embedder ready", "kind", embedder.Name(), "dim", embedder.Dim())

	skillSvc := skills.NewService(st.Skills, embedder, cfg.Skills.Dir, log)
	if cfg.Skills.SyncOnStartup {
		if res, err := skillSvc.Sync(ctx); err != nil {
			log.Warn("startup skill sync failed", "err", err)
		} else {
			log.Info("startup skill sync done", "discovered", res.Discovered, "embedded", res.Embedded)
		}
	}

	registry, err := llm.NewRegistry(cfg.LLM)
	if err != nil {
		log.Error("llm registry failed", "err", err)
		os.Exit(1)
	}
	for name, pc := range cfg.LLM.Providers {
		if pc.Kind == "fake" {
			registry.Register(name, fakeprovider.New(fakeSkillSlug()))
			log.Info("registered fake provider", "name", name)
		}
	}
	if err := registry.Validate(); err != nil {
		log.Error("llm registry invalid", "err", err)
		os.Exit(1)
	}

	toolReg, err := buildToolRegistry(skillSvc, cfg)
	if err != nil {
		log.Error("tool registry failed", "err", err)
		os.Exit(1)
	}

	var mcpMgr *appmcp.Manager
	if cfg.MCP.Enabled {
		mcpMgr = appmcp.New(toolReg, log, cfg.MCP.ToolPrefix, cfg.MCP.InitTimeout)
		defer mcpMgr.Close()
		bootstrapMCP(ctx, mcpMgr, st, cfg, log)
		if cfg.MCP.File != "" && cfg.MCP.ReloadInterval > 0 {
			go appmcp.Watcher(ctx, mcpMgr, cfg.MCP.File, cfg.MCP.ReloadInterval)
		}
		log.Info("mcp enabled", "attached", len(mcpMgr.List()), "file", cfg.MCP.File,
			"reload_interval", cfg.MCP.ReloadInterval)
	}

	ag := agent.New(st, registry, toolReg, skillSvc, cfg.Agent, cfg.LLM, log)

	handlers := &api.Handlers{
		Store:    st,
		Agent:    ag,
		Registry: registry,
		Skills:   skillSvc,
		MCP:      mcpMgr,
		LLM:      cfg.LLM,
		Agentcfg: cfg.Agent,
		Log:      log,
	}

	hz := server.New(cfg.HTTP, handlers, log)
	log.Info("bepilot listening", "addr", cfg.HTTP.Addr, "env", cfg.Env, "default_provider", cfg.LLM.DefaultProvider)
	server.Run(ctx, hz)
	log.Info("shutdown complete")
}

func fakeSkillSlug() string {
	if v := os.Getenv("BEPILOT_FAKE_SKILL_SLUG"); v != "" {
		return v
	}
	return "pdf-forms"
}
