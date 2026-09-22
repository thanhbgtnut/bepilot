-- +goose Up
-- Structured per-task output (e.g. a skill's checklist review or information
-- extraction), kept separate from the message transcript so a report UI can
-- read it directly, independent of the agent/LLM pipeline that produced it.
CREATE TABLE task_results (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    task_key   text NOT NULL,
    title      text NOT NULL,
    result     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, task_key)
);
CREATE INDEX task_results_session_idx ON task_results (session_id);

-- +goose Down
DROP TABLE IF EXISTS task_results;
