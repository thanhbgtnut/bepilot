-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "citext";

CREATE TABLE users (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email      citext NOT NULL UNIQUE,
    name       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL DEFAULT '',
    key_prefix   text NOT NULL,
    key_hash     bytea NOT NULL,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_prefix_idx ON api_keys (key_prefix) WHERE revoked_at IS NULL;
CREATE INDEX api_keys_user_idx ON api_keys (user_id);

CREATE TABLE sessions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title           text NOT NULL DEFAULT '',
    provider        text NOT NULL DEFAULT '',
    model           text NOT NULL DEFAULT '',
    system_override text NOT NULL DEFAULT '',
    summary         text NOT NULL DEFAULT '',
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX sessions_user_updated_idx ON sessions (user_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE messages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq         integer NOT NULL,
    role        text NOT NULL,
    stop_reason text NOT NULL DEFAULT '',
    usage       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, seq)
);
CREATE INDEX messages_session_seq_idx ON messages (session_id, seq);

CREATE TABLE content_blocks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id  uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    idx         integer NOT NULL,
    type        text NOT NULL,
    text        text NOT NULL DEFAULT '',
    thinking    text NOT NULL DEFAULT '',
    tool_name   text NOT NULL DEFAULT '',
    tool_use_id text NOT NULL DEFAULT '',
    tool_input  jsonb,
    tool_result jsonb,
    is_error    boolean NOT NULL DEFAULT false,
    UNIQUE (message_id, idx)
);

CREATE TABLE agent_runs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_id  uuid REFERENCES messages(id) ON DELETE SET NULL,
    provider    text NOT NULL DEFAULT '',
    model       text NOT NULL DEFAULT '',
    steps       integer NOT NULL DEFAULT 0,
    tokens_in   integer NOT NULL DEFAULT 0,
    tokens_out  integer NOT NULL DEFAULT 0,
    latency_ms  integer NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_runs_session_idx ON agent_runs (session_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS agent_runs;
DROP TABLE IF EXISTS content_blocks;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
