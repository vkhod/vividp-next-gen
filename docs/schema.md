# VividP — Schema Design Narrative

> This document explains the schema in terms of *what* each table models and *why*
> it is designed the way it is. For the DDL, see `db/migrations/001_jobs.sql`.
> For *why* specific decisions were made, see `docs/decisions.md`.

---

## Schema Sources

The schema was derived from three sources:

1. **A real legacy job XML file** — revealed the actual field structure,
   five value layers per field, two bounding box systems, and phase timestamps.

2. **`fsjob_json.pas` Delphi source** — the legacy Delphi unit used to serialize
   jobs to JSON. Revealed: `system_id`, `job_name` vs `job_alias` distinction,
   `skipped_verify`/`skipped_trutypist` flags, `operator_rotation` (VerifyRotate),
   `setup_table`, `field_order`/`is_visible`/`is_readonly`, `error_comment`,
   `user_data`, `content_hash` (__meta pattern), `is_last_in_form` (bLastInForm).

3. **Architectural design** — Job Service pattern, NATS dual subjects,
   fractional page ordering, document topology as first-class entity.

---

## Table: jobs

The central entity. One row per document job from the moment a file is
detected until it is archived after export.

### Why so many columns?

Legacy jobs carry a lot of lifecycle metadata that doesn't fit neatly into
"just a state machine." The schema reflects the reality of production IDP:

- **Six phase timestamps** (`capture_began_at` through `verification_ended_at`)
  because OCR can run months after capture in backlog processing scenarios.
  A single `updated_at` would lose this information.

- **Two error columns** (`last_error` for the technical exception string,
  `error_comment` for the human-written explanation) because operators and
  supervisors write explanations separately from what the system logs.

- **Two name columns** (`job_name` = file path, `job_alias` = display name)
  because these are genuinely different concepts — conflating them causes
  display and file system problems.

- **Three skip/duplicate flags** (`skipped_verify`, `skipped_trutypist`,
  `is_duplicate`) because a job showing COMPLETED is not necessarily
  human-reviewed. Analytics and SLA reporting depend on knowing which
  pipeline stages were actually executed.

### JSONB columns on jobs

```
classifications[]     Ranked document type candidates. Array supports multiple
                      candidates — jcn1/jct1/jcc1 through jcnN pattern.
                      Rank 1 is the winner.

job_state{}           The pipeline accumulator. Every station merges its output
                      here using PostgreSQL || operator. Never replaced, only grown.
                      Example after ingestion:
                      {"detected_at":"...","source":"invoice.pdf",
                       "worker_id":"ingest-1","page_count":3,"ingestion_ms":420}

artifacts[]           Manifest of all files written to MinIO for this job.
                      Appended as each file is confirmed written.
                      Example entry:
                      {"key":"jobs/acme/uuid/pages/001/original.tif",
                       "type":"original_tif","page_num":1,"size_bytes":4218880}

pipeline_timings{}    Per-station duration in milliseconds, recorded at each
                      state transition. Avoids computing durations from timestamp
                      differences at query time.
                      Example: {"ingest":420,"classify":850,"match":210}
```


---

## Table: job_documents

Models the logical structure of a batch job. The legacy system encoded document
boundaries as boolean flags on individual pages (`rpginfo.end_doc`). This
required scanning all page flags to understand the batch topology.

`job_documents` makes document structure explicit:
- A batch of 3 invoices = 3 rows, one per logical document
- Pages belong to a document via `document_id` FK
- Document reorder = update `document_index` on one row
- Bundle grouping = `bundle_name` column

The `page_count` column is maintained automatically by a PostgreSQL trigger
(`sync_document_page_count`) whenever pages are inserted, deleted, or soft-deleted.

---

## Table: job_pages

One row per page within a job.

### Page ordering — fractional indexing

`order_key FLOAT8` is the display sort key. Always `ORDER BY order_key`.

```
Initial assignment: 1.0, 2.0, 3.0, 4.0
Move page 3 before 1: UPDATE job_pages SET order_key = 0.5 WHERE id = $page3_id
Insert between 1 and 2: set order_key = 1.5
```

Exactly one row updated per reorder. `original_page_index` is immutable after
ingestion — always records where the page was in the original file.

Rebalance (reset to 1.0, 2.0, 3.0...) when any gap falls below 0.001.

### operator_rotation

This is `VerifyRotate` from the Delphi source. It is rotation applied by the
**human operator during Verification** — they looked at the page in the browser
and clicked a rotate button. It is NOT scan rotation or preprocessing rotation.

When a verified job is reopened, this value must be applied on top of the
already-registered TIF file. Preprocessing rotation is baked into
`jobs/{id}/pages/N/registered.tif`. These are different transformations.

Scan/preprocessing rotation goes in `preprocessing{}` JSONB:
```json
{"deskewed": true, "deskew_angle_deg": 0.8, "rotation_applied": 0}
```

### FSA original values

`fsa_form`, `fsa_original_page_no`, `fsa_original_template`, `fsa_original_state`
preserve the state of the page before any post-processing changes. These are the
legacy archive originals — needed for audit trails.

### JSONB columns on job_pages

```
match_candidates[]    All template match candidates, not just the winner.
                      Enables debugging template matching errors and reprocessing.
                      Example: [{"template":"General","confidence":100,"type":6},
                                 {"template":"Extended","confidence":72,"type":3}]

preprocessing{}       Image operations applied to produce registered.tif.
                      Example: {"deskewed":true,"deskew_angle_deg":0.8,
                                 "enhanced":false,"binarized":false}
```

---

## Table: job_fields

One row per recognized field value. The richest table in the schema.

### Hot relational columns (queried constantly by Verification Workstation)

```sql
-- Primary Verification WS query: all visible fields for a page
SELECT field_name, final_value, field_state, confidence,
       setup_table, array_index, is_readonly,
       geometry->'image' AS bbox,
       setup_info->>'description' AS label
FROM job_fields
WHERE page_id = $1 AND is_visible = TRUE
ORDER BY field_order, array_index NULLS FIRST;
```

These columns must stay relational for index efficiency:
- `field_state` — for finding all not_validated fields
- `confidence` — for routing below-threshold fields to verification
- `field_order` — for correct display sequence
- `is_visible` — for filtering system/computed fields from the UI
- `setup_table` — for grouping line items into their tables
- `array_index` — for ordering line item rows

### Five value layers in recognition{} JSONB

All engine outputs are preserved. Never collapsed to just final_value:

```json
{
  "ocr": {
    "raw": "31/10/25",
    "normalized": "20251031",
    "confidence": 70,
    "engine": "tesseract",
    "version": "5.3.2",
    "duration_ms": 12
  },
  "llm": {
    "raw": "20251031",
    "normalized": "20251031",
    "model": "claude-3-sonnet",
    "duration_ms": 340,
    "prompt_tokens": 120
  },
  "operator": {
    "corrected_at": "2025-11-13T14:04:22Z",
    "previous_value": "20251030",
    "correction_count": 1
  }
}
```

Adding a new engine (e.g. AWS Textract) requires no schema migration —
just add a `"textract"` key to the recognition object.

### Two bounding box systems in geometry{} JSONB

```json
{
  "image":    {"left": 219, "top": 670, "right": 368, "bottom": 708},
  "template": {"left": 316, "top": 240, "right": 464, "bottom": 262}
}
```

`image` coordinates: where the field appears on THIS specific scanned page.
Used by the Verification Workstation to draw the yellow highlight box.
Changes with every document scan.

`template` coordinates: canonical zone position on the template definition.
Used by the Template Editor and for geometric registration.
Never changes — fixed to the form definition.

### setup_table — line item grouping

`setup_table` is `ARField.SetupField.SetupTable.Name` from the Delphi source.
It groups repeating fields into their named table: "Items", "Taxes", "Shipping".

Without this column, the Verification Workstation cannot render line item tables
correctly — it cannot know which fields belong to which table, or how many
distinct tables exist in a document.

Query to get all line items grouped by table:
```sql
SELECT setup_table, array_index, field_name, final_value, field_state
FROM job_fields
WHERE job_id = $1 AND array_index IS NOT NULL
ORDER BY setup_table, array_index, field_order;
```

---

## Table: job_transitions

Immutable audit log. INSERT only — no UPDATE, no DELETE, ever.

Records every state change with: `from_status`, `to_status`, `worker_id`,
`note`, `occurred_at`. Enables full replay of any job's lifecycle.

---

## Table: job_stats

Analytics written once when a job reaches COMPLETED status. Never updated.

The key insight: a job showing COMPLETED may have bypassed human review
entirely. `was_verify_skipped` and `was_typist_skipped` (denormalized from
`jobs.skipped_verify` and `jobs.skipped_trutypist`) make this explicit in
reporting without requiring a JOIN back to the `jobs` table for every query.

`cross_page_issues[]` JSONB captures the class of problem found in the
sample XML: `CustomerID` on page 1 = `570045799`, on page 2 = `570015362`.
OCR got `9999` on both pages. LLM extracted different values per page.
This inconsistency is detected by the Validation Service and recorded here
for operator review and model retraining signal.

---

## Pending: 002_systems.sql

`jobs.system_id` is currently a TEXT column (no FK constraint). The System
Registry table is the next migration to write. A legacy System is the
XML/binary configuration entity that defines:
- Which forms and templates are available
- Field definitions, types, validation rules per field
- Recognition strategy per field type
- Export targets and formats
- Verification workflow configuration

Every job belongs to exactly one System. The system_id FK is the link that
tells the pipeline which form definitions, templates, and rules to apply.

Two System types:
- **Legacy** — wraps an existing legacy XML/binary configuration
  (used during the Strangler Fig migration period)
- **Native** — a first-class 2.0 system definition stored in the registry

---

## Key Indexes

```sql
-- Job queue dispatch (most frequent production query)
idx_jobs_tenant_status    ON jobs(tenant_id, status)
idx_jobs_priority         ON jobs(priority DESC, created_at)
                          WHERE on_hold = FALSE AND status NOT IN (...)

-- Worker claim (SELECT FOR UPDATE SKIP LOCKED)
idx_jobs_claimed          ON jobs(status, claimed_by) WHERE claimed_by IS NULL

-- Verification Workstation (highest-frequency read in the system)
idx_fields_page           ON job_fields(page_id, field_order)
idx_fields_visible        ON job_fields(page_id, field_order)
                          WHERE is_visible = TRUE AND is_job_level = FALSE

-- Line item queries
idx_fields_array          ON job_fields(job_id, setup_table, array_index)
                          WHERE array_index IS NOT NULL

-- Page display order
idx_pages_job_order       ON job_pages(job_id, order_key)
idx_pages_active          ON job_pages(job_id, order_key) WHERE is_deleted = FALSE
```
