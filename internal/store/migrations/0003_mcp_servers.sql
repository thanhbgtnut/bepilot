-- +goose Up
-- MCP servers registered dynamically through the /v1/mcp API. Servers declared
-- in the config file (mcp.file) are NOT stored here; this table only backs the
-- API so runtime-added servers survive a restart.
CREATE TABLE mcp_servers (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL UNIQUE,
    transport  text NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    spec       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS mcp_servers;
