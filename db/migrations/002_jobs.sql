-- ═══════════════════════════════════════════════════════════════════════════════
-- FormStorm Next Gen — Full Schema v4
-- Sources: real job XML analysis + fsjob_json.pas Delphi + architectural design
-- ═══════════════════════════════════════════════════════════════════════════════

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: jobs
-- Central entity. One row per document job from detection through archival.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS jobs (

    -- ── Identity ──────────────────────────────────────────────────────────────
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    legacy_job_id       TEXT        UNIQUE,         -- FormStorm 1.0 job_id e.g. "3020251104082502"
    fs_uid              TEXT,                       -- ARJob.uid from Delphi (migration helper)
    tenant_id           UUID        NOT NULL REFERENCES tenants(id),
    system_id           UUID        NOT NULL REFERENCES systems(id),
    
    -- ── Naming (two distinct concepts from Delphi) ────────────────────────────
    job_name            TEXT,   -- ARJob.JobFileName — actual file path on disk
    job_alias           TEXT,   -- ARJob.JobName     — human-readable display name

    -- ── State machine ─────────────────────────────────────────────────────────
    status              TEXT        NOT NULL DEFAULT 'DETECTED',
    stage               TEXT        NOT NULL DEFAULT 'INGESTION',
    fs_status           INT,        -- legacy FormStorm numeric status (migration helper)
    fs_state            INT,        -- legacy FormStorm numeric state  (migration helper)

    -- ── Capture metadata ──────────────────────────────────────────────────────
    capture_type        INT,
    metadata_override   BOOLEAN     NOT NULL DEFAULT FALSE, -- mdatao from XML

    -- ── Source files ──────────────────────────────────────────────────────────
    source_filename     TEXT        NOT NULL,
    source_bucket       TEXT        NOT NULL,
    source_key          TEXT        NOT NULL,
    size_bytes          BIGINT      NOT NULL DEFAULT 0,
    original_file_path  TEXT,                       -- full Windows/Linux path (audit)
    nfiles              INT         NOT NULL DEFAULT 1, -- number of source files in job

    -- ── Page counts ───────────────────────────────────────────────────────────
    page_count          INT,
    scanned_pages       INT,                        -- ScannedPages from XML
    suspected_fields_count INT,                     -- pre-computed, used for queue prioritization

    -- ── Queue management ──────────────────────────────────────────────────────
    priority            INT         NOT NULL DEFAULT 0,   -- higher = processed first
    on_hold             BOOLEAN     NOT NULL DEFAULT FALSE,-- operator-set pause
    on_error            INT         NOT NULL DEFAULT 0,   -- 0=retry 1=skip 2=escalate
    filter              TEXT,
    user_data           TEXT,                       -- ARJob.JobUserData — free-form external context

    -- ── Job flags ─────────────────────────────────────────────────────────────
    -- IMPORTANT: a COMPLETED job may never have had human review.
    -- These flags make that explicit for analytics and SLA reporting.
    skipped_verify      BOOLEAN     NOT NULL DEFAULT FALSE, -- verification bypassed entirely
    skipped_trutypist   BOOLEAN     NOT NULL DEFAULT FALSE, -- typist entry bypassed
    is_duplicate        BOOLEAN     NOT NULL DEFAULT FALSE,
    is_archived         BOOLEAN     NOT NULL DEFAULT FALSE,

    -- ── Export paths ──────────────────────────────────────────────────────────
    export_data_path    TEXT,
    export_image_path   TEXT,
    tif_file_key        TEXT,       -- MinIO key for primary TIF
    tif_reg_file_key    TEXT,       -- MinIO key for registered/processed TIF

    -- ── Verifier metrics (v_name, v_time, nksv from FormStorm 1.0 XML) ───────
    verifier_name           TEXT,
    verification_seconds    INT,
    keystrokes_count        INT,

    -- ── Six independent phase timestamps ─────────────────────────────────────
    -- Each phase is timed separately — OCR can run months after capture (backlog).
    capture_began_at        TIMESTAMPTZ,    -- cptb
    capture_ended_at        TIMESTAMPTZ,    -- cpte
    ocr_began_at            TIMESTAMPTZ,    -- ocrb
    ocr_ended_at            TIMESTAMPTZ,    -- ocre
    verification_began_at   TIMESTAMPTZ,    -- vrfb
    verification_ended_at   TIMESTAMPTZ,    -- vrfe

    -- ── Error information (two separate fields from Delphi) ───────────────────
    last_error          TEXT,   -- technical exception string (except_message)
    error_comment       TEXT,   -- human-written explanation   (except_comment)

    -- ── Integrity ─────────────────────────────────────────────────────────────
    -- SHA2 of job JSON at last state change — __meta.checksum pattern from TJSBase
    content_hash        TEXT,
    hash_computed_at    TIMESTAMPTZ,

    -- ── JSONB accumulators ────────────────────────────────────────────────────
    -- classifications: ranked document type candidates (jcn1/jct1/jcc1 pattern)
    -- e.g. [{"rank":1,"name":"Invoice","type":1,"confidence":50}]
    classifications     JSONB       NOT NULL DEFAULT '[]',

    -- job_state: pipeline accumulator — each station MERGES its output here.
    -- NEVER replace, always: job_state = job_state || $new::jsonb
    job_state           JSONB       NOT NULL DEFAULT '{}',

    -- artifacts: MinIO file manifest, appended as each file is confirmed written
    -- e.g. [{"key":"jobs/t/id/pages/001/original.tif","type":"original_tif",...}]
    artifacts           JSONB       NOT NULL DEFAULT '[]',

    -- pipeline_timings: per-station duration in ms, recorded at each transition
    -- e.g. {"ingest":420,"classify":850,"match":210}
    pipeline_timings    JSONB       NOT NULL DEFAULT '{}',

    -- ── Worker claim ──────────────────────────────────────────────────────────
    retry_count         INT         NOT NULL DEFAULT 0,
    claimed_by          TEXT,
    claimed_at          TIMESTAMPTZ,

    -- ── Timestamps ────────────────────────────────────────────────────────────
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ
);

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: job_documents
-- Logical document grouping within a batch job.
-- REPLACES rpginfo end_doc/end_bundle boolean flags from FormStorm 1.0.
-- A batch of 3 invoices = 3 rows here. Pages belong via document_id FK.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS job_documents (

    id                          UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id                      UUID    NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,

    document_index              INT     NOT NULL,   -- current order (operator may reorder)
    original_document_index     INT     NOT NULL,   -- immutable — position at ingestion

    form_name                   TEXT,
    template_name               TEXT,
    bundle_name                 TEXT,
    page_count                  INT     NOT NULL DEFAULT 0, -- maintained by trigger
    is_complete                 BOOLEAN NOT NULL DEFAULT FALSE,

    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (job_id, document_index)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: job_pages
-- One row per page within a job.
--
-- ORDERING: order_key FLOAT8 uses fractional indexing.
--   Initial: 1.0, 2.0, 3.0
--   Reorder page 3 before 1: set order_key = 0.5  (ONE row updated)
--   Insert between 1 and 2: set order_key = 1.5
--   Always: ORDER BY job_id, order_key
--
-- operator_rotation: VerifyRotate from Delphi — rotation applied by the operator
--   IN the Verification Workstation. NOT scan rotation. NOT preprocessing rotation.
--   Stored separately so revisiting a job shows it as the operator left it.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS job_pages (

    id                      BIGSERIAL   PRIMARY KEY,
    job_id                  UUID        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    document_id             UUID        REFERENCES job_documents(id) ON DELETE SET NULL,

    -- ── Position ──────────────────────────────────────────────────────────────
    original_page_index     INT         NOT NULL,   -- immutable after ingestion
    source_page_number      INT,                    -- page number within source PDF
    order_key               FLOAT8      NOT NULL,   -- fractional ordering key — ORDER BY this

    -- ── State ─────────────────────────────────────────────────────────────────
    state                   TEXT,
    fs_pstate               INT,        -- legacy FormStorm integer state code (migration helper)
    is_deleted              BOOLEAN     NOT NULL DEFAULT FALSE,

    -- ── Page role in batch ────────────────────────────────────────────────────
    is_separator            BOOLEAN     NOT NULL DEFAULT FALSE,
    is_attached             BOOLEAN     NOT NULL DEFAULT FALSE,
    is_last_in_form         BOOLEAN     NOT NULL DEFAULT FALSE, -- bLastInForm from Delphi
    color_bar_check         INT         DEFAULT 0,
    was_new_batch           BOOLEAN     NOT NULL DEFAULT FALSE,

    -- ── Operator rotation — NOT scan rotation ─────────────────────────────────
    -- VerifyRotate from Delphi. Applied by human operator in Verification UI.
    -- Preprocessing/scan rotation goes in preprocessing{} JSONB instead.
    operator_rotation       INT         NOT NULL DEFAULT 0,

    -- ── Template matching ─────────────────────────────────────────────────────
    setup_page_no           INT,
    template_name           TEXT,
    original_template       TEXT,       -- org_tmpl — before operator override
    master_page_name        TEXT,       -- mpn e.g. "inv"
    match_type              INT,
    match_confidence        INT,        -- 0–100
    ftwt_candidates         INT         DEFAULT 0,

    -- ── Image geometry — critical for Verification Workstation ────────────────
    -- Used to correctly scale and position field highlight boxes on screen
    image_height_px         INT,        -- imgh from XML
    image_width_px          INT,        -- imgw from XML

    -- ── File references ───────────────────────────────────────────────────────
    original_file_path      TEXT,       -- orig_file_name (Windows path preserved)
    reassigned_form_name    TEXT,       -- nf_name — operator reassigned this page
    reassigned_form_page    INT,        -- nf_page_index
    export_data_filename    TEXT,       -- per-page export destination (exp_data_filename)
    export_image_filename   TEXT,       -- per-page export destination (exp_image_filename)
    except_message          TEXT,       -- per-page exception (page can fail independently)

    -- ── FSA archive originals (state before post-processing) ──────────────────
    fsa_form                TEXT,       -- fsaofrm
    fsa_original_page_no    INT,        -- fsaorgpn
    fsa_original_template   TEXT,       -- fsaotmpln
    fsa_original_state      TEXT,       -- fsaorgst

    -- ── CMC7 / MICR — banking/cheque documents ────────────────────────────────
    cmc7_flags              INT         DEFAULT 0,
    cmc7                    TEXT,

    -- ── JSONB ─────────────────────────────────────────────────────────────────
    -- match_candidates: all template match candidates, not just the winner
    match_candidates        JSONB       NOT NULL DEFAULT '[]',
    -- preprocessing: image operations applied to produce registered.tif
    -- e.g. {"deskewed":true,"deskew_angle_deg":0.8,"enhanced":false}
    preprocessing           JSONB       NOT NULL DEFAULT '{}',

    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (job_id, order_key)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: job_fields
-- One row per recognized field value.
--
-- FIVE VALUE LAYERS — all preserved, none collapsed:
--   final_value (relational)       — accepted value, typist_content from XML
--   recognition.ocr.raw            — exactly what OCR engine returned
--   recognition.ocr.normalized     — after format normalization (org_ocr → ocr_content)
--   recognition.llm.raw            — exactly what LLM returned
--   recognition.llm.normalized     — after normalization
--   recognition.operator.*         — operator correction record
--
-- TWO BOUNDING BOX SYSTEMS — both required:
--   geometry.image    — where field appears on THIS scanned page (Verification WS)
--   geometry.template — canonical zone on template definition (Template Editor)
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS job_fields (

    id              BIGSERIAL   PRIMARY KEY,
    job_id          UUID        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    page_id         BIGINT      REFERENCES job_pages(id) ON DELETE CASCADE, -- NULL for job-level fields

    -- ── Field identity ────────────────────────────────────────────────────────
    fs_uid          TEXT,       -- ARField.uid from Delphi (migration helper)
    field_name      TEXT        NOT NULL,
    array_index     INT,        -- 1-based row for repeating fields; NULL for non-repeating
    -- Line-item table grouping: ARField.SetupField.SetupTable.Name
    -- e.g. "Items", "Taxes", "Shipping" — critical for Verification WS table rendering
    setup_table     TEXT,
    is_job_level    BOOLEAN     NOT NULL DEFAULT FALSE, -- TRUE = rjobpage scope

    -- ── Hot columns — RELATIONAL for Verification WS query performance ────────
    final_value     TEXT,       -- accepted final value (typist_content)
    field_state     TEXT,       -- recognized | validated | not_validated | ''
    value_source    TEXT,       -- ocr | llm | operator | default
    confidence      INT,        -- 0–100 from OCR engine; NULL if not OCR-processed

    -- ── UI display properties — queried constantly by Verification WS ─────────
    field_order     INT         NOT NULL DEFAULT 0,  -- SetupField.FieldOrder — display sequence
    is_visible      BOOLEAN     NOT NULL DEFAULT TRUE, -- FALSE = DisplayIf=DI_NEVER
    is_readonly     BOOLEAN     NOT NULL DEFAULT FALSE, -- bReadOnly from Delphi

    -- ── JSONB: all engine outputs ─────────────────────────────────────────────
    -- Keys: "ocr", "llm", "operator" — add new engines without schema migration
    -- "ocr":  {"raw":"31/10/25","normalized":"20251031","confidence":70,"engine":"tesseract","duration_ms":12}
    -- "llm":  {"raw":"20251031","normalized":"20251031","model":"claude-3-sonnet","duration_ms":340}
    -- "operator": {"corrected_at":"...","previous_value":"...","correction_count":1}
    recognition     JSONB       NOT NULL DEFAULT '{}',

    -- ── JSONB: both bounding box systems ─────────────────────────────────────
    -- "image":    {"left":219,"top":670,"right":368,"bottom":708}   ← Verification WS bbox
    -- "template": {"left":316,"top":240,"right":464,"bottom":262}   ← Template Editor bbox
    geometry        JSONB,

    -- ── JSONB: setup/template properties ─────────────────────────────────────
    -- {"description":"Invoice Number","field_type":"text",
    --  "html_id":"InvoiceNumber","dictionary":"#invoice_numbers","group_index":0}
    setup_info      JSONB       NOT NULL DEFAULT '{}',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: job_transitions
-- Immutable audit log. INSERT only — never UPDATE, never DELETE.
-- Complete history of every state change with worker identity.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS job_transitions (

    id              BIGSERIAL   PRIMARY KEY,
    job_id          UUID        NOT NULL REFERENCES jobs(id),
    from_status     TEXT,
    to_status       TEXT        NOT NULL,
    worker_id       TEXT,
    note            TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: job_stats
-- Analytics written ONCE at job completion. Never updated after write.
-- Feeds dashboards, operator productivity reports, engine quality monitoring.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS job_stats (

    id              BIGSERIAL   PRIMARY KEY,
    job_id          UUID        NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE CASCADE,

    -- ── Phase durations ───────────────────────────────────────────────────────
    ingest_duration_ms          INT,
    classification_duration_ms  INT,
    matching_duration_ms        INT,
    recognition_duration_ms     INT,
    validation_duration_ms      INT,
    verification_duration_ms    INT,
    export_duration_ms          INT,
    total_duration_ms           INT,    -- wall clock: detected → completed

    -- ── Field quality ─────────────────────────────────────────────────────────
    total_fields                INT,
    recognized_fields           INT,
    auto_validated_fields       INT,
    operator_corrected_fields   INT,
    operator_confirmed_fields   INT,
    auto_rejected_fields        INT,
    empty_fields                INT,

    -- ── Operator efficiency ───────────────────────────────────────────────────
    keystrokes_per_field        NUMERIC(6,2),
    seconds_per_field           NUMERIC(6,2),
    rekey_count                 INT,
    tab_count                   INT,

    -- ── Skip tracking (denormalized from jobs for reporting efficiency) ───────
    was_verify_skipped          BOOLEAN NOT NULL DEFAULT FALSE,
    was_typist_skipped          BOOLEAN NOT NULL DEFAULT FALSE,

    -- ── Engine quality ────────────────────────────────────────────────────────
    ocr_avg_confidence          NUMERIC(5,2),
    llm_avg_confidence          NUMERIC(5,2),
    ocr_llm_agreement_rate      NUMERIC(5,2),   -- % where OCR and LLM matched
    ocr_used_as_final           INT,
    llm_used_as_final           INT,
    operator_used_as_final      INT,

    -- ── JSONB analytics ───────────────────────────────────────────────────────
    -- engine_stats: per-engine breakdown
    -- e.g. {"tesseract":{"field_count":12,"avg_confidence":84.2,"total_ms":450}}
    engine_stats                JSONB   NOT NULL DEFAULT '{}',

    -- validation_results: which rules fired and their outcomes
    -- e.g. {"math_balance":{"checked":true,"passed":true},"date_format":{"passed":false}}
    validation_results          JSONB   NOT NULL DEFAULT '{}',

    -- cross_page_issues: fields resolving differently across pages of the same job
    -- e.g. [{"field":"CustomerID","page_1":"570045799","page_2":"570015362",
    --         "resolution":"operator_corrected","final":"570045799"}]
    cross_page_issues           JSONB   NOT NULL DEFAULT '[]',

    computed_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ═══════════════════════════════════════════════════════════════════════════════
-- INDEXES
-- ═══════════════════════════════════════════════════════════════════════════════

-- ── jobs ──────────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_jobs_source 
    ON jobs (source_bucket, source_key);

CREATE INDEX idx_jobs_tenant_status
    ON jobs (tenant_id, status);

CREATE INDEX idx_jobs_system
    ON jobs (system_id, status);

-- Priority queue dispatch — excludes jobs on hold or in terminal states
CREATE INDEX idx_jobs_priority
    ON jobs (priority DESC, created_at)
    WHERE on_hold = FALSE
      AND status NOT IN ('COMPLETED','DEAD_LETTER','FAILED');

-- Worker claim pattern — SELECT FOR UPDATE SKIP LOCKED
CREATE INDEX idx_jobs_claimed
    ON jobs (status, claimed_by)
    WHERE claimed_by IS NULL;

CREATE INDEX idx_jobs_status_updated
    ON jobs (status, updated_at);

CREATE INDEX idx_jobs_legacy
    ON jobs (legacy_job_id)
    WHERE legacy_job_id IS NOT NULL;

CREATE INDEX idx_jobs_verifier
    ON jobs (verifier_name)
    WHERE verifier_name IS NOT NULL;

CREATE INDEX idx_jobs_duplicate
    ON jobs (tenant_id, is_duplicate)
    WHERE is_duplicate = TRUE;

CREATE INDEX idx_jobs_on_hold
    ON jobs (tenant_id, on_hold)
    WHERE on_hold = TRUE;

-- JSONB indexes for classification and state queries
CREATE INDEX idx_jobs_state_gin
    ON jobs USING GIN (job_state);

CREATE INDEX idx_jobs_classifications_gin
    ON jobs USING GIN (classifications);

CREATE INDEX idx_jobs_artifacts_gin
    ON jobs USING GIN (artifacts);

-- ── job_documents ─────────────────────────────────────────────────────────────
CREATE INDEX idx_docs_job
    ON job_documents (job_id, document_index);

CREATE INDEX idx_docs_bundle
    ON job_documents (job_id, bundle_name)
    WHERE bundle_name IS NOT NULL;

-- ── job_pages ─────────────────────────────────────────────────────────────────
-- Primary access pattern: all pages for a job in current display order
CREATE INDEX idx_pages_job_order
    ON job_pages (job_id, order_key);

CREATE INDEX idx_pages_document
    ON job_pages (document_id, order_key);

-- Active (non-deleted) pages only — most common filter
CREATE INDEX idx_pages_active
    ON job_pages (job_id, order_key)
    WHERE is_deleted = FALSE;

-- ── job_fields ────────────────────────────────────────────────────────────────
-- Verification Workstation primary query: visible fields for a page in order
CREATE INDEX idx_fields_page
    ON job_fields (page_id, field_order);

CREATE INDEX idx_fields_visible
    ON job_fields (page_id, field_order)
    WHERE is_visible = TRUE AND is_job_level = FALSE;

-- All fields for a job (includes job-level fields)
CREATE INDEX idx_fields_job
    ON job_fields (job_id, field_name);

-- Verification routing: low-confidence fields
CREATE INDEX idx_fields_confidence
    ON job_fields (job_id, confidence)
    WHERE confidence IS NOT NULL;

-- Validation state queries
CREATE INDEX idx_fields_state
    ON job_fields (job_id, field_state);

-- Line item queries: all rows of a repeating field group by table
CREATE INDEX idx_fields_array
    ON job_fields (job_id, setup_table, array_index)
    WHERE array_index IS NOT NULL;

-- Job-level fields only (rjobpage scope)
CREATE INDEX idx_fields_job_level
    ON job_fields (job_id)
    WHERE is_job_level = TRUE;

-- JSONB queries — OCR/LLM disagreement detection
CREATE INDEX idx_fields_recognition_gin
    ON job_fields USING GIN (recognition);

-- ── job_transitions ───────────────────────────────────────────────────────────
CREATE INDEX idx_transitions_job
    ON job_transitions (job_id, occurred_at);

CREATE INDEX idx_transitions_worker
    ON job_transitions (worker_id, occurred_at);

-- ── job_stats ─────────────────────────────────────────────────────────────────
CREATE INDEX idx_stats_computed
    ON job_stats (computed_at)
    INCLUDE (total_duration_ms, operator_corrected_fields, ocr_llm_agreement_rate);

-- ═══════════════════════════════════════════════════════════════════════════════
-- TRIGGERS
-- ═══════════════════════════════════════════════════════════════════════════════

-- Auto-update updated_at on every row change
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER jobs_updated_at
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER fields_updated_at
    BEFORE UPDATE ON job_fields
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- Auto-maintain document page_count when pages are inserted, deleted, or soft-deleted
CREATE OR REPLACE FUNCTION sync_document_page_count()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' AND NEW.document_id IS NOT NULL THEN
        UPDATE job_documents SET page_count = page_count + 1
        WHERE id = NEW.document_id;

    ELSIF TG_OP = 'DELETE' AND OLD.document_id IS NOT NULL THEN
        UPDATE job_documents SET page_count = GREATEST(0, page_count - 1)
        WHERE id = OLD.document_id;

    ELSIF TG_OP = 'UPDATE'
        AND OLD.is_deleted != NEW.is_deleted
        AND NEW.document_id IS NOT NULL THEN
        UPDATE job_documents
        SET page_count = page_count + (CASE WHEN NEW.is_deleted THEN -1 ELSE 1 END)
        WHERE id = NEW.document_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER pages_sync_doc_count
    AFTER INSERT OR UPDATE OF is_deleted OR DELETE ON job_pages
    FOR EACH ROW EXECUTE FUNCTION sync_document_page_count();

-- ═══════════════════════════════════════════════════════════════════════════════
-- USEFUL REFERENCE QUERIES
-- ═══════════════════════════════════════════════════════════════════════════════

-- All pages for a job in current display order:
-- SELECT p.id, p.order_key, p.original_page_index, p.state,
--        p.image_height_px, p.image_width_px, d.form_name
-- FROM job_pages p
-- LEFT JOIN job_documents d ON d.id = p.document_id
-- WHERE p.job_id = $1 AND p.is_deleted = FALSE
-- ORDER BY d.document_index, p.order_key;

-- All visible fields for a page (Verification WS):
-- SELECT f.field_name, f.final_value, f.field_state, f.confidence,
--        f.setup_table, f.array_index, f.is_readonly,
--        f.geometry->'image' AS bbox,
--        f.recognition->'ocr'->>'normalized' AS ocr_val,
--        f.recognition->'llm'->>'normalized' AS llm_val,
--        f.setup_info->>'description' AS label
-- FROM job_fields f
-- WHERE f.page_id = $1 AND f.is_visible = TRUE
-- ORDER BY f.field_order, f.array_index NULLS FIRST;

-- Fields where OCR and LLM disagreed (model quality signal):
-- SELECT field_name, final_value, value_source,
--        recognition->'ocr'->>'normalized' AS ocr_val,
--        recognition->'llm'->>'normalized' AS llm_val
-- FROM job_fields
-- WHERE job_id = $1
--   AND recognition ? 'ocr' AND recognition ? 'llm'
--   AND recognition->'ocr'->>'normalized' != recognition->'llm'->>'normalized';

-- Priority queue — next jobs to process:
-- SELECT id, job_alias, system_id, priority, suspected_fields_count,
--        skipped_verify, is_duplicate, status
-- FROM jobs
-- WHERE status = 'INGESTED' AND on_hold = FALSE AND tenant_id = $1
-- ORDER BY priority DESC, created_at ASC;

-- Reorder a page (fractional indexing — only ONE row updated):
-- UPDATE job_pages SET order_key = 1.5 WHERE id = $page_id;

-- Operator productivity report:
-- SELECT j.verifier_name,
--        COUNT(*) AS jobs_verified,
--        SUM(CASE WHEN j.skipped_verify THEN 1 ELSE 0 END) AS auto_completed,
--        AVG(s.seconds_per_field) AS avg_secs_per_field,
--        AVG(s.ocr_llm_agreement_rate) AS avg_agreement_rate
-- FROM jobs j JOIN job_stats s ON s.job_id = j.id
-- WHERE j.verifier_name IS NOT NULL
--   AND j.completed_at > NOW() - INTERVAL '30 days'
-- GROUP BY j.verifier_name ORDER BY avg_secs_per_field;
