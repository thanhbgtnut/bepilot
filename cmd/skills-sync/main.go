// Command skills-sync rescans the skills directory and reconciles the database
// (upsert, re-embed changed, delete removed). Equivalent to POST /v1/skills/sync.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/logging"
	"github.com/thanhenti/bepilot/internal/retrieval"
	"github.com/thanhenti/bepilot/internal/skills"
	"github.com/thanhenti/bepilot/internal/store"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := logging.New(cfg.Log)

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db:", err)
		os.Exit(1)
	}
	defer st.Close()

	embedder, err := retrieval.New(cfg.Embd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "embedder:", err)
		os.Exit(1)
	}
	svc := skills.NewService(st.Skills, embedder, cfg.Skills.Dir, log)
	res, err := svc.Sync(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
}
