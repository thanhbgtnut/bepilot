// Package store is the persistence layer: a pgx connection pool, embedded
// migrations, and hand-written repositories for each aggregate.
package store

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/thanhenti/bepilot/internal/config"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store bundles the connection pool and all repositories.
type Store struct {
	Pool     *pgxpool.Pool
	Users    *UsersRepo
	APIKeys  *APIKeysRepo
	Sessions *SessionsRepo
	Messages *MessagesRepo
	Skills   *SkillsRepo
	Runs     *AgentRunsRepo
	MCP      *MCPServersRepo
}

// Open creates the pool and, when cfg.DB.AutoMigrate is set, runs migrations.
func Open(ctx context.Context, cfg config.DB) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	if cfg.AutoMigrate {
		if err := Migrate(cfg.DSN); err != nil {
			pool.Close()
			return nil, err
		}
	}

	return &Store{
		Pool:     pool,
		Users:    &UsersRepo{pool},
		APIKeys:  &APIKeysRepo{pool},
		Sessions: &SessionsRepo{pool},
		Messages: &MessagesRepo{pool},
		Skills:   &SkillsRepo{pool},
		Runs:     &AgentRunsRepo{pool},
		MCP:      &MCPServersRepo{pool},
	}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.Pool.Close() }

// Migrate applies all embedded up-migrations against the given DSN. It is safe
// to call repeatedly.
func Migrate(dsn string) error {
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: open db: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// ensure the pgx stdlib driver is linked for goose.
var _ = stdlib.GetDefaultDriver
