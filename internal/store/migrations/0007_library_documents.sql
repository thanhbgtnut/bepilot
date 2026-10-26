-- +goose Up
-- Uploaded documents (/v1/documents), read in the background page by page.
-- The table is also the work queue: a worker claims a row (FOR UPDATE SKIP
-- LOCKED), holds it with a lease it keeps renewing (lease_token,
-- locked_until), and a row whose lease ran out is claimed again by another
-- worker, which reads only the pages still missing from library_pages.
CREATE TABLE library_documents (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name               text NOT NULL DEFAULT '',
    media_type         text NOT NULL DEFAULT '',
    size_bytes         bigint NOT NULL,
    sha256             text NOT NULL,
    data               bytea NOT NULL,
    status             text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'ready', 'failed')),
    pages_total        integer NOT NULL DEFAULT 0,
    pages_done         integer NOT NULL DEFAULT 0,
    failed_pages       integer NOT NULL DEFAULT 0,
    unverified_numbers integer NOT NULL DEFAULT 0,
    token_count        integer NOT NULL DEFAULT 0,
    error              text NOT NULL DEFAULT '',
    attempts           integer NOT NULL DEFAULT 0,
    run_after          timestamptz NOT NULL DEFAULT now(),
    lease_token        uuid,
    locked_until       timestamptz,
    finished_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX library_documents_user_idx ON library_documents (user_id, created_at DESC);
CREATE INDEX library_documents_user_sha_idx ON library_documents (user_id, sha256);
CREATE INDEX library_documents_queue_idx ON library_documents (created_at) WHERE status IN ('pending', 'processing');

-- What was read from each page: the markdown given to the agent (numbers
-- confirmed with OCR, the rest noted unverified), the OCR text, and the
-- number check.
CREATE TABLE library_pages (
    document_id uuid NOT NULL REFERENCES library_documents(id) ON DELETE CASCADE,
    page_no     integer NOT NULL,
    markdown    text NOT NULL DEFAULT '',
    ocr_text    text NOT NULL DEFAULT '',
    numbers     jsonb,
    unverified  integer NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT '',
    tokens      integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, page_no)
);

-- The documents of a session: every turn of the session gets their full text.
CREATE TABLE session_documents (
    session_id  uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    document_id uuid NOT NULL REFERENCES library_documents(id) ON DELETE CASCADE,
    attached_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (session_id, document_id)
);
CREATE INDEX session_documents_document_idx ON session_documents (document_id);

-- +goose Down
DROP TABLE IF EXISTS session_documents;
DROP TABLE IF EXISTS library_pages;
DROP TABLE IF EXISTS library_documents;
