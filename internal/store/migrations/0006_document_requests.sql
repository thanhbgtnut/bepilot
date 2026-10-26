-- +goose Up
-- One row per call to a /v1/documents/* endpoint: what was asked, what
-- docpipe read from the files, and what was answered. The row is written when
-- the request arrives (status 'processing') and finished when it ends, so a
-- request that crashed half way still shows up.
CREATE TABLE document_requests (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind                 text NOT NULL CHECK (kind IN ('parse', 'message')),
    status               text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'ok', 'error')),
    -- The request as sent, with file contents replaced by their document_files id.
    request              jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- docpipe output per file: media type and, per page, markdown, OCR text, error.
    result               jsonb,
    -- The response body returned to the client (for a stream, the message it built).
    response             jsonb,
    error                text NOT NULL DEFAULT '',
    session_id           uuid REFERENCES sessions(id) ON DELETE SET NULL,
    user_message_id      uuid REFERENCES messages(id) ON DELETE SET NULL,
    assistant_message_id uuid REFERENCES messages(id) ON DELETE SET NULL,
    latency_ms           integer NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX document_requests_user_idx ON document_requests (user_id, created_at DESC);
CREATE INDEX document_requests_session_idx ON document_requests (session_id) WHERE session_id IS NOT NULL;

-- The uploaded files themselves, so a request can be replayed.
CREATE TABLE document_files (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL REFERENCES document_requests(id) ON DELETE CASCADE,
    idx        integer NOT NULL,
    name       text NOT NULL DEFAULT '',
    media_type text NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL,
    sha256     text NOT NULL,
    data       bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (request_id, idx)
);

-- +goose Down
DROP TABLE IF EXISTS document_files;
DROP TABLE IF EXISTS document_requests;
