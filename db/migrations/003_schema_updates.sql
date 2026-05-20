-- ── 003_schema_updates.sql ────────────────────────────────────────────────────
-- Adds folder_accumulation table for multi-instance safe folder-mode ingestion.
-- Replaces the in-memory FolderAccumulator map with a shared PostgreSQL table.
-- ─────────────────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS folder_accumulation (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    prefix       TEXT        NOT NULL,                    -- MinIO folder prefix, e.g. tenants/t/s/input/batch001
    file_key     TEXT        NOT NULL,                    -- full MinIO object key of the file
    filename     TEXT        NOT NULL,                    -- base name of the file
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    tenant_id    UUID        NOT NULL,
    system_id    UUID        NOT NULL,
    bucket       TEXT        NOT NULL DEFAULT 'input',
    ready        BOOLEAN     NOT NULL DEFAULT FALSE,      -- TRUE once _READY signal has arrived
    meta_content TEXT,                                    -- content of _READY.json (NULL for regular files)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (prefix, file_key)
);

-- Index for the two hot queries:
--   1. "all files for this prefix" (Add + Signal)
--   2. "expired rows" (TTL janitor)
CREATE INDEX IF NOT EXISTS idx_folder_accum_prefix   ON folder_accumulation (prefix);
CREATE INDEX IF NOT EXISTS idx_folder_accum_created  ON folder_accumulation (created_at) WHERE ready = FALSE;
