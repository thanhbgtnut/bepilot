-- +goose Up
-- What a turn actually did, beyond the totals: the sampling settings it ran
-- with, the tools it called, how it ended. Two runs of the same request can then
-- be compared instead of guessed at.
ALTER TABLE agent_runs ADD COLUMN detail jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE agent_runs DROP COLUMN IF EXISTS detail;
