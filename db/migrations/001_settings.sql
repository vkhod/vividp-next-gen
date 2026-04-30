-- ═══════════════════════════════════════════════════════════════════════════
-- VividP — Settings & Multi-Tenant Schema
-- Migration 002: tenants, systems, station configs, document types, fields
--
-- Derived from real HapoalimClassification.xml system configuration.
-- Designed for multi-tenant: multiple tenants, each with multiple systems,
-- coexisting in a single deployment. Strict isolation: one tenant per system.
--
-- See docs/decisions.md ADR-013 for rationale.
-- ═══════════════════════════════════════════════════════════════════════════

-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: tenants
-- The organizational boundary. One row per client organization.
-- In a single-tenant on-prem deployment, exactly one row exists.
-- In a multi-tenant cloud deployment, one row per customer.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS tenants (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Human-readable unique identifier — used in URLs, NATS subjects, MinIO paths
    -- e.g. "hapoalim", "leumi", "bituach-leumi"
    -- Immutable after creation (referenced in storage paths)
    slug            TEXT        NOT NULL UNIQUE,

    -- Display name — mutable
    name            TEXT        NOT NULL,

    -- ── Storage configuration ─────────────────────────────────────────────
    -- Where this tenant's artifacts live. Bucket names, key prefixes, etc.
    -- On-prem: typically {"bucket_prefix": "hapoalim", "region": "local"}
    -- Cloud: {"bucket_prefix": "hapoalim", "region": "il-central-1", ...}
    storage_config  JSONB       NOT NULL DEFAULT '{}',

    -- ── Compliance & policy ───────────────────────────────────────────────
    -- Data retention days, encryption requirements, cloud-service permissions
    -- e.g. {"retention_days": 365, "allow_cloud_engines": false,
    --        "allow_llm_services": false, "encryption_at_rest": true}
    compliance_config JSONB     NOT NULL DEFAULT '{}',

    -- ── Feature flags ─────────────────────────────────────────────────────
    -- Tenant-level feature toggles
    -- e.g. {"ivo_enabled": true, "classify_enabled": true, "invoices_enabled": false}
    features        JSONB       NOT NULL DEFAULT '{}',

    -- ── Lifecycle ─────────────────────────────────────────────────────────
    active          BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: systems
-- The central configuration entity — maps 1:1 to the <system> XML root.
-- Each system defines a complete processing personality: which engines,
-- what templates, what forms/fields, what validation rules.
-- A tenant can have multiple systems (e.g. classification, invoices, checks).
--
-- Supports two types:
--   'legacy'  — pointer to existing XML/binary config (Win32 wrapper)
--   'native'  — new structured config in child tables
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS systems (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),

    -- Machine-readable code — unique within the tenant
    -- e.g. "hapoalim-classification", "hapoalim-invoices"
    code            TEXT        NOT NULL,

    -- Display name — from XML <system name="HapoalimClassification">
    name            TEXT        NOT NULL,

    -- 'legacy' or 'native' — determines how config is consumed
    system_type     TEXT        NOT NULL DEFAULT 'native'
                    CHECK (system_type IN ('legacy', 'native')),

    -- ── Legacy support ────────────────────────────────────────────────────
    -- For system_type='legacy': filesystem path to the XML/binary config
    -- The Win32 wrapper consumes this as-is via the Strangler Fig adapter
    -- NULL for native systems
    legacy_config_path  TEXT,

    -- ── Global system-level config ────────────────────────────────────────
    -- For system_type='native': the ~40 attributes from the <system> root
    -- All processing flags, timers, priority strategies, etc.
    --
    -- Mapped from HapoalimClassification.xml root attributes:
    -- {
    --   "use_template": true,
    --   "ignore_below": 70,          -- confidence threshold
    --   "rotate_charset_in_ocr": 2,
    --   "rotate_charset": 2,
    --   "auto_rot_in_ocr": true,
    --   "auto_form_in_ocr": false,
    --   "default_page": "0200:1",
    --   "bin_pri": true,              -- prioritization flags
    --   "ocr_pri": true,
    --   "ver_pri": true,
    --   "has_bundles": true,
    --   "timers": {
    --     "verify": -1,               -- tmrver
    --     "trutypist": -1,            -- tmrtru
    --     "level1": 150,              -- trmlv1
    --     "level2": 240               -- trmlv2
    --   }
    -- }
    global_config   JSONB       NOT NULL DEFAULT '{}',

    -- ── Scripting hooks (stored as strings for now — ADR pending) ─────────
    -- From XML: on_change_ftype, qe_sync, qe_stcomplete, etc.
    -- In the future these become VWD workflow step references or
    -- registered handler plugins. For now, just the names.
    -- e.g. {"on_change_ftype": "HaPoalimClassification.VerifyChangingForm",
    --        "qe_sync": "poalim_tools.OnSyncSLA",
    --        "qe_stcomplete": "HaPoalimClassification.DeleteFilesResultOCR"}
    hooks           JSONB       NOT NULL DEFAULT '{}',

    -- ── Versioning ────────────────────────────────────────────────────────
    -- Incremented on every config save. Jobs record which version they ran under.
    version         INT         NOT NULL DEFAULT 1,

    -- ── Lifecycle ─────────────────────────────────────────────────────────
    active          BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- System code is unique within a tenant
    UNIQUE (tenant_id, code)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: system_versions
-- Immutable audit log of system configuration snapshots.
-- Every time a system config is saved, the previous state is captured here.
-- Used for: compliance ("what config was active when batch X ran"),
--           rollback, and debugging.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS system_versions (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    system_id       UUID        NOT NULL REFERENCES systems(id),
    version         INT         NOT NULL,

    -- Full snapshot of the system's global_config + hooks at this version
    snapshot        JSONB       NOT NULL,

    -- Who made the change
    changed_by      TEXT,
    change_note     TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (system_id, version)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: station_configs
-- Per-station processing parameters within a system.
-- Maps directly to <parameters>/<input|ocr|verify|export> in the XML.
--
-- Each system has one row per station. The config JSONB is heterogeneous
-- because each station cares about completely different things.
-- Validated by JSON Schema at the application layer, not in the DB.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS station_configs (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    system_id       UUID        NOT NULL REFERENCES systems(id),

    -- Station identifier: 'ingestion', 'recognition', 'verification', 'export'
    -- Maps from XML: input → ingestion, ocr → recognition, verify → verification
    station         TEXT        NOT NULL
                    CHECK (station IN ('ingestion', 'recognition', 'verification', 'export')),

    -- ── Station-specific configuration ────────────────────────────────────
    --
    -- INGESTION (from XML <input>):
    -- {
    --   "resolution": 300,
    --   "page_width": 8.5,
    --   "page_height": 11.7,
    --   "brightness": 55,
    --   "remove_lines": false,
    --   "adaptive_threshold": true,
    --   "priority_scheme": ["03-Low", "02-Med", "01-High"]
    -- }
    --
    -- RECOGNITION (from XML <ocr>):
    -- {
    --   "default_languages": 102,
    --   "default_template": "Default",
    --   "template_condition": "T",
    --   "image_enhancement": "/d",
    --   "temp_image_enhancement": "/h1",
    --   "full_page": true,
    --   "apply_gfr": true,
    --   "keep_characters": true
    -- }
    --
    -- VERIFICATION (from XML <verify>):
    -- {
    --   "rtl": true,
    --   "page_zoom": 70,
    --   "highlight_color": 8388863,
    --   "not_legal_color": 11592668,
    --   "highlight_not_legal": true,
    --   "field_label_font_size": 12,
    --   "field_data_font_size": 14,
    --   "field_label_font_color": 16711680,
    --   "max_dictionary_items": 100,
    --   "auto_next_job": true,
    --   "use_field_descriptions": true,
    --   "table_below_area_percent": 50,
    --   "verify_invoices": true,
    --   "out_of_context_verify": true,
    --   "out_of_context_verify2": true,
    --   "out_of_context_supervisor": true
    -- }
    --
    -- EXPORT (from XML <export>):
    -- {
    --   "export_to_db": true,
    --   "export_format": "None",
    --   "export_original_images": false,
    --   "export_registered_images": true,
    --   "source_pdf_mode": 2
    -- }
    config          JSONB       NOT NULL DEFAULT '{}',

    -- Station-level hooks (from XML on_start_job, on_end_page, etc.)
    -- Stored as strings for now — future VWD integration
    hooks           JSONB       NOT NULL DEFAULT '{}',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One config per station per system
    UNIQUE (system_id, station)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: document_types
-- Each <form> in the XML becomes a row here.
-- Three special types serve inheritance roles:
--   is_job_form=true   → _JobForm: job-level metadata fields (always present)
--   is_global=true     → _GlobalForm: fields injected into every document type
--   is_default=true    → _Default: fallback for unclassified documents
--
-- Resolution order when assembling a complete field set:
--   _JobForm fields → _GlobalForm fields → document-type-specific fields
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS document_types (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    system_id       UUID        NOT NULL REFERENCES systems(id),

    -- Document type code — from XML <form name="0101">
    -- Unique within the system
    code            TEXT        NOT NULL,

    -- Human-readable name — from XML description attribute
    -- e.g. "ביטול חיים", "העברת שכר ישראל"
    name            TEXT,

    -- ── Special form roles ────────────────────────────────────────────────
    -- At most one of each per system
    is_job_form     BOOLEAN     NOT NULL DEFAULT FALSE,
    is_global       BOOLEAN     NOT NULL DEFAULT FALSE,
    is_default      BOOLEAN     NOT NULL DEFAULT FALSE,

    -- ── Extraction strategy ───────────────────────────────────────────────
    -- Determines how the Recognition Hub approaches this document type.
    --
    -- 'content_first' — (default, modern path)
    --   Full-page OCR first, then classify by content, then extract fields
    --   using label patterns, dictionaries, and masks from field_definitions.
    --   No template images needed. This is what gen_labels/gen_mask/gen_dict
    --   already do in the legacy XML — it just wasn't the primary path.
    --
    -- 'zone_based' — (legacy structured forms)
    --   Match template image → OCR specific bounding box regions.
    --   Requires templates with zone coordinates.
    --   Optimal for fixed-layout government/bank forms where field positions
    --   never change and targeted OCR is faster/more accurate.
    --
    -- 'hybrid' — (best of both)
    --   Full-page OCR for classification + content extraction,
    --   but zone hints from templates used to improve accuracy when available.
    --   Graceful degradation: works without zones, better with them.
    --
    extraction_strategy TEXT    NOT NULL DEFAULT 'content_first'
                    CHECK (extraction_strategy IN ('content_first', 'zone_based', 'hybrid')),

    -- ── Form-level configuration ──────────────────────────────────────────
    -- From XML form attributes: allow_globalf_first, visible, etc.
    -- e.g. {"allow_global_first": true, "visible": true}
    config          JSONB       NOT NULL DEFAULT '{}',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Document type code unique within system
    UNIQUE (system_id, code)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: field_definitions
-- One row per field within a document type.
-- Maps directly to <field> elements in the XML.
--
-- The XML field element carries 30+ attributes. We extract the most
-- commonly queried ones as typed columns and pack the rest into JSONB
-- for flexibility.
--
-- This table defines the TEMPLATE — what fields exist and how they behave.
-- Actual recognized VALUES live in job_fields (001_jobs.sql).
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS field_definitions (

    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    document_type_id    UUID        NOT NULL REFERENCES document_types(id),

    -- ── Identity ──────────────────────────────────────────────────────────
    -- field name from XML — unique within the document type
    name                TEXT        NOT NULL,

    -- field_index from XML — the global numeric ID (e.g. 294, 628)
    -- Used for cross-referencing with legacy system data
    field_index         INT,

    -- field_order — display/processing sequence
    field_order         INT         NOT NULL DEFAULT 0,

    -- page_no from XML — which page this field appears on
    page_no             INT         NOT NULL DEFAULT 1,

    -- ── Type & recognition ────────────────────────────────────────────────
    -- type from XML: 'Alpha numeric', 'Numeric', 'Date'
    field_type          TEXT        NOT NULL DEFAULT 'Alpha numeric',

    -- default_ocr_type from XML: 'InvMachine', etc.
    default_ocr_type    TEXT,

    -- def_reg_type from XML: 'normal', 'DDMMYYYY', etc.
    default_reg_type    TEXT,

    -- ── Constraints ───────────────────────────────────────────────────────
    max_len             INT,
    min_len             INT,

    -- ── Display behavior ──────────────────────────────────────────────────
    -- display_if / display_if2 from XML: 'always', 'never', etc.
    -- Controls field visibility at different stations
    display_if          TEXT        DEFAULT 'always',
    display_if2         TEXT,

    -- dis_len from XML — display width in characters
    display_length      INT,

    -- description from XML — Hebrew label shown to operators
    description         TEXT,

    -- export_field_order — order in exported data
    export_field_order  INT,

    -- export flag — "0" means exclude from export
    exportable          BOOLEAN     NOT NULL DEFAULT TRUE,

    -- ── Table membership ──────────────────────────────────────────────────
    -- table_name from XML — if set, this field is a line-item column
    -- Null means it's a standalone (header) field
    table_name          TEXT,

    -- ── Recognition configuration (JSONB) ─────────────────────────────────
    -- Everything about HOW this field gets recognized:
    -- {
    --   "dictionary": "InsuranceCompany.dct",
    --   "dict_col": 1,
    --   "group_id": 4,
    --   "group_dict": "accounts_hapoalim.dct",
    --   "group_dict_col": 2,
    --   "auto_group_correct": true,
    --   "look_gen": true,               -- generic field recognition
    --   "gen_ft": 52,                   -- generic field type
    --   "gen_dict": "TypeBitul.dct",    -- dictionary for generic recognition
    --   "gen_mask": "###-####|...",     -- pattern masks
    --   "gen_labels": "סניף-חשבון:|...",-- label text patterns
    --   "gen_nl": "...",                -- negative labels (stop patterns)
    --   "gen_sl": "...",                -- supplementary labels
    --   "gen_minc": 3,                  -- min character count
    --   "gen_maxc": 10,                 -- max character count
    --   "gen_decimals": 1,
    --   "min_dict_mark": 850,           -- minimum dictionary match score
    --   "ignore_case_dict": true,
    --   "jlf_params": 0,               -- JLF recognition parameters
    --   "label_is_not_exact": false,
    --   "label_rtl": true,
    --   "label_required": false,
    --   "bexd": true,                   -- boundary extraction
    --   "snippet": true                 -- extract snippet image
    -- }
    recognition_config  JSONB       NOT NULL DEFAULT '{}',

    -- ── Validation configuration (JSONB) ───────────────────────────────────
    -- Rules applied after recognition, before verification:
    -- {
    --   "validation_func": "HaPoalimClassification.ValidationLastPageFormBitul",
    --   "on_next": "HaPoalimClassification.OnNextEventID",
    --   "on_change": "HaPoalimClassification.SetValueZrufa",
    --   "on_ooc": "HaPoalimClassification.TotalCliQ",
    --   "date_type": "DDMMYYYY",
    --   "date_post_processing": true,
    --   "price_post_processing": true,
    --   "longest_post_processing": true,
    --   "omr_post_processing": true,
    --   "mod10": true,                  -- Luhn check digit validation
    --   "special_chars": "0-9.,",
    --   "value_required": true,
    --   "value_indicator": true
    -- }
    validation_config   JSONB       NOT NULL DEFAULT '{}',

    -- ── Display / verification UI config (JSONB) ──────────────────────────
    -- How the field appears in the Verification Workstation:
    -- {
    --   "default_value": "סוג מסמך / מקט",
    --   "read_only": true,
    --   "disabled": true,
    --   "read_only_color": 16776960,
    --   "right_to_left": true,
    --   "hide_description": true,
    --   "no_conflicts": true,
    --   "show_in_both_stations": true,   -- show12
    --   "eol_before": true,              -- line break before field
    --   "eol_after": true,               -- line break after field
    --   "autofill": true,
    --   "partial_match": true,
    --   "auto_on_change": true,          -- aoc
    --   "search_after_dictionary_init": true  -- sadi
    -- }
    display_config      JSONB       NOT NULL DEFAULT '{}',

    -- ── Min/max array (line items) ────────────────────────────────────────
    -- For fields that repeat as line items
    min_array           INT,
    max_array           INT,

    -- ── Alias ─────────────────────────────────────────────────────────────
    -- alias from XML — alternative name for export/scripting reference
    -- e.g. "dbfield_4", "BranchNoCharge", "TotalAmount"
    alias               TEXT,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Field name unique within document type
    UNIQUE (document_type_id, name)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: templates
-- Template definitions for document matching and zone-based extraction.
-- Maps to <template> elements nested inside <form>/<pages>/<page>.
--
-- DESIGN NOTE — Template role depends on extraction_strategy:
--
--   content_first:  Templates are OPTIONAL. Classification and field extraction
--                   happen via full-page OCR + content analysis. The field
--                   definitions (gen_labels, gen_mask, gen_dict) ARE the
--                   extraction recipe. Templates may still exist for reference
--                   or legacy migration but are not used in the hot path.
--
--   zone_based:     Templates carry zone coordinates (bounding boxes) for each
--                   field. The Recognition Hub uses image matching to pick the
--                   right template, then OCRs specific regions. This is the
--                   legacy path — optimal for fixed-layout forms.
--                   Template images stored in MinIO/S3.
--
--   hybrid:         Full-page OCR for classification, but zone hints from
--                   templates improve extraction accuracy when available.
--                   Degrades gracefully: works without zones, better with them.
--
-- For content_first document types, this table may have zero rows — and
-- that's perfectly fine. The field_definitions.recognition_config IS the
-- extraction recipe (labels, masks, dictionaries).
--
-- For zone_based types, the image_key points to the reference TIF in MinIO
-- and zone_coordinates holds the per-field bounding boxes.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS templates (

    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    document_type_id    UUID        NOT NULL REFERENCES document_types(id),

    -- Template name from XML — e.g. "Any0101", "20230215171251315"
    name                TEXT        NOT NULL,

    -- Page number this template belongs to (from XML nesting position)
    page_no             INT         NOT NULL DEFAULT 1,

    -- ── Image reference (zone_based / hybrid only) ────────────────────────
    -- MinIO/S3 object key for the template image
    -- NULL for content_first templates (no image needed)
    image_key           TEXT,

    -- no_match from XML — if true, this is a "catch-all" template
    -- (used when the document doesn't match any specific template)
    no_match            BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Original filename from XML (migration reference)
    original_filename   TEXT,

    -- ── Zone coordinates (zone_based / hybrid only) ───────────────────────
    -- Per-field bounding boxes on this template's reference image.
    -- Used by the Recognition Hub for targeted zone OCR.
    -- NULL/empty for content_first templates.
    -- {
    --   "field_name": {"left": 350, "top": 120, "right": 580, "bottom": 155},
    --   ...
    -- }
    zone_coordinates    JSONB       NOT NULL DEFAULT '{}',

    -- ── Metadata ──────────────────────────────────────────────────────────
    metadata            JSONB       NOT NULL DEFAULT '{}',

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Template name unique within document type
    UNIQUE (document_type_id, name)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: table_definitions
-- Line-item table structures within a document type.
-- Maps to <table> elements in the XML.
-- Fields belonging to a table have table_name set in field_definitions.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS table_definitions (

    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    document_type_id    UUID        NOT NULL REFERENCES document_types(id),

    -- Table name from XML — e.g. "NameAndID", "TaxClearanceTable"
    name                TEXT        NOT NULL,

    -- Page this table belongs to
    page_no             INT         NOT NULL DEFAULT 1,

    -- ── Array bounds ──────────────────────────────────────────────────────
    min_rows            INT         NOT NULL DEFAULT 1,
    max_rows            INT         NOT NULL DEFAULT 50,
    min_cols            INT,

    -- ── Table behavior ────────────────────────────────────────────────────
    -- From XML: exact_cells, grid, hide_verify, hide_trutypist, force_ood
    config              JSONB       NOT NULL DEFAULT '{}',

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (document_type_id, name)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: exception_types
-- Available exception actions operators can apply during verification.
-- Maps to <exceptions>/<exception> in the XML.
-- Each exception has visibility flags per station (verify, verify2, supervisor, etc.)
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS exception_types (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    system_id       UUID        NOT NULL REFERENCES systems(id),

    -- Exception name from XML — e.g. "Illegal Field Value", "Cancel Page"
    name            TEXT        NOT NULL,

    -- Severity level from XML — 1=field, 2=page/document
    level           INT         NOT NULL DEFAULT 1,

    -- Station visibility flags
    -- From XML: bMatching, bVerify, bVerify2, bSupervisor, bQC
    -- {
    --   "matching": true,
    --   "verify": true,
    --   "verify2": true,
    --   "supervisor": true,
    --   "qc": false
    -- }
    station_visibility  JSONB   NOT NULL DEFAULT '{}',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (system_id, name)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- TABLE: queues
-- Routing and prioritization within a system.
-- For now, system-level only (no per-queue config overrides).
-- The VWD module will later add workflow inheritance at the queue level.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS queues (

    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    system_id       UUID        NOT NULL REFERENCES systems(id),

    -- Queue name
    name            TEXT        NOT NULL,

    -- ── Routing ───────────────────────────────────────────────────────────
    -- Rules that determine which jobs enter this queue
    -- e.g. {"document_types": ["0101", "0103"], "priority_min": 1}
    routing_rules   JSONB       NOT NULL DEFAULT '{}',

    -- ── Priority ──────────────────────────────────────────────────────────
    -- Queue-level priority strategy
    -- e.g. {"default_priority": 2, "sla_seconds": 3600}
    priority_config JSONB       NOT NULL DEFAULT '{}',

    active          BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (system_id, name)
);


-- ─────────────────────────────────────────────────────────────────────────────
-- NOTE: Update jobs table (001_jobs.sql) directly
-- Since we're in planning phase, update 001_jobs.sql to change:
--   tenant_id  TEXT  →  tenant_id  UUID  REFERENCES tenants(id)
--   system_id  TEXT  →  system_id  UUID  REFERENCES systems(id)
-- No ALTER TABLE needed — we define the whole schema fresh.
-- ─────────────────────────────────────────────────────────────────────────────


-- ─────────────────────────────────────────────────────────────────────────────
-- INDEXES
-- ─────────────────────────────────────────────────────────────────────────────

-- tenants
CREATE INDEX idx_tenants_slug           ON tenants (slug);
CREATE INDEX idx_tenants_active         ON tenants (active) WHERE active = TRUE;

-- systems
CREATE INDEX idx_systems_tenant         ON systems (tenant_id);
CREATE INDEX idx_systems_tenant_active  ON systems (tenant_id, active) WHERE active = TRUE;
CREATE INDEX idx_systems_type           ON systems (system_type);

-- station_configs: fast lookup by system + station
CREATE INDEX idx_station_configs_system ON station_configs (system_id);

-- document_types
CREATE INDEX idx_doctypes_system        ON document_types (system_id);
CREATE INDEX idx_doctypes_system_code   ON document_types (system_id, code);
CREATE INDEX idx_doctypes_global        ON document_types (system_id, is_global) WHERE is_global = TRUE;
CREATE INDEX idx_doctypes_strategy      ON document_types (system_id, extraction_strategy);

-- field_definitions
CREATE INDEX idx_fields_doctype         ON field_definitions (document_type_id);
CREATE INDEX idx_fields_index           ON field_definitions (field_index);
CREATE INDEX idx_fields_table           ON field_definitions (document_type_id, table_name)
                                        WHERE table_name IS NOT NULL;

-- templates
CREATE INDEX idx_templates_doctype      ON templates (document_type_id);

-- table_definitions
CREATE INDEX idx_tabledefs_doctype      ON table_definitions (document_type_id);

-- exception_types
CREATE INDEX idx_exceptions_system      ON exception_types (system_id);

-- queues
CREATE INDEX idx_queues_system          ON queues (system_id);

-- system_versions: find latest version quickly
CREATE INDEX idx_sysver_system_version  ON system_versions (system_id, version DESC);


-- ─────────────────────────────────────────────────────────────────────────────
-- SEED: Default tenant and system for local development
-- Matches the docker-compose.yml local infrastructure setup.
-- ─────────────────────────────────────────────────────────────────────────────

INSERT INTO tenants (id, slug, name, storage_config, compliance_config, features)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'dev',
    'Local Development',
    '{"bucket_prefix": "", "region": "local"}',
    '{"retention_days": 0, "allow_cloud_engines": true, "allow_llm_services": true}',
    '{"ivo_enabled": true, "classify_enabled": true, "invoices_enabled": true}'
) ON CONFLICT (slug) DO NOTHING;

INSERT INTO systems (id, tenant_id, code, name, system_type, global_config)
VALUES (
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000001',
    'default',
    'Default System',
    'native',
    '{
        "use_template": true,
        "ignore_below": 70,
        "rotate_charset_in_ocr": 2,
        "auto_rot_in_ocr": true,
        "auto_form_in_ocr": false,
        "has_bundles": false,
        "timers": {"verify": -1, "trutypist": -1, "level1": 150, "level2": 240}
    }'
) ON CONFLICT (tenant_id, code) DO NOTHING;

-- Default station configs for the dev system
INSERT INTO station_configs (system_id, station, config) VALUES
    ('00000000-0000-0000-0000-000000000002', 'ingestion',
     '{"resolution": 300, "page_width": 8.5, "page_height": 11.7, "brightness": 55, "remove_lines": false, "adaptive_threshold": true}'),
    ('00000000-0000-0000-0000-000000000002', 'recognition',
     '{"default_languages": 102, "default_template": "Default", "image_enhancement": "/d", "full_page": true, "keep_characters": true}'),
    ('00000000-0000-0000-0000-000000000002', 'verification',
     '{"rtl": false, "page_zoom": 70, "max_dictionary_items": 100, "auto_next_job": true, "use_field_descriptions": true}'),
    ('00000000-0000-0000-0000-000000000002', 'export',
     '{"export_to_db": true, "export_format": "None", "export_original_images": false, "export_registered_images": true}')
ON CONFLICT (system_id, station) DO NOTHING;
