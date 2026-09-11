package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhenti/bepilot/internal/domain"
)

// MCPServersRepo persists MCP servers added through the API. File-declared
// servers are not stored here.
type MCPServersRepo struct{ pool *pgxpool.Pool }

// List returns every persisted MCP server, ordered by name.
func (r *MCPServersRepo) List(ctx context.Context) ([]domain.MCPServer, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, transport, enabled, spec, created_at, updated_at
		FROM mcp_servers ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("mcp_servers.List: %w", err)
	}
	defer rows.Close()

	var out []domain.MCPServer
	for rows.Next() {
		var s domain.MCPServer
		if err := rows.Scan(&s.ID, &s.Name, &s.Transport, &s.Enabled,
			&metaScanner{&s.Spec}, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns one server by name.
func (r *MCPServersRepo) Get(ctx context.Context, name string) (domain.MCPServer, error) {
	var s domain.MCPServer
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, transport, enabled, spec, created_at, updated_at
		FROM mcp_servers WHERE name = $1`, name).
		Scan(&s.ID, &s.Name, &s.Transport, &s.Enabled, &metaScanner{&s.Spec}, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MCPServer{}, ErrNotFound
	}
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("mcp_servers.Get: %w", err)
	}
	return s, nil
}

// Upsert inserts or replaces a server by name.
func (r *MCPServersRepo) Upsert(ctx context.Context, name, transport string, enabled bool, spec map[string]any) (domain.MCPServer, error) {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("mcp_servers.Upsert marshal: %w", err)
	}
	var s domain.MCPServer
	err = r.pool.QueryRow(ctx, `
		INSERT INTO mcp_servers (name, transport, enabled, spec)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE
		SET transport = EXCLUDED.transport, enabled = EXCLUDED.enabled,
		    spec = EXCLUDED.spec, updated_at = now()
		RETURNING id, name, transport, enabled, spec, created_at, updated_at`,
		name, transport, enabled, specJSON).
		Scan(&s.ID, &s.Name, &s.Transport, &s.Enabled, &metaScanner{&s.Spec}, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("mcp_servers.Upsert: %w", err)
	}
	return s, nil
}

// Delete removes a server by name. It returns ErrNotFound when nothing matched.
func (r *MCPServersRepo) Delete(ctx context.Context, name string) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM mcp_servers WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("mcp_servers.Delete: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
