# VividP — Architectural Decision Log

> This document records **why** we made specific design choices.
> Before proposing a change to any of the areas below, read the relevant
> section to understand the reasoning. Many of these decisions replaced
> something that existed in the legacy system.

---

## ADR-001: Job created at detection, not after conversion

**Decision:** Insert the job row into PostgreSQL the moment a file is detected
by the webhook handler — before downloading, before conversion, before anything.

**Why:** The legacy system had no concept of a job until the queue folder was fully
created. If anything failed during ingestion, the job simply disappeared with no
trace. There was no way to know a file had been detected and lost.

**Consequences:**
- Job can always be found and recovered even if the system crashes mid-conversion
- Retry logic has a starting point — Temporal can detect any job stuck in DETECTED
  or INGESTING longer than a threshold
- Analytics are accurate — every file that entered the system is counted, even failures

---

## ADR-002: Job Service as sole writer to jobs table

**Decision:** No pipeline service writes directly to the `jobs`, `job_documents`,
`job_pages`, or `job_fields` tables. All access goes through `job.Service`.

**Why:** Without a single point of control:
- State transition rules would need to be duplicated in every service
- Illegal transitions (skipping states, going backwards) would be possible
- Audit logging would require remembering to add it everywhere
- Adding a hook (metrics, alerts, billing) would touch every service

**Consequences:**
- `job.Service` is the most critical piece of infrastructure in the system
- It must be fast, reliable, and tested thoroughly
- gRPC endpoint in production — not just a local Go call

---

## ADR-003: rpginfo replaced by job_documents + fractional page ordering

**Decision:** Replace the legacy `rpginfo` boolean flags
(`end_doc`, `end_bundle`, `sep`, `org_end_doc`, etc.) with:
1. An explicit `job_documents` table for document grouping
2. `order_key FLOAT8` on `job_pages` for page ordering

**Why rpginfo was problematic:**
- Document structure was encoded as flags on individual pages — you had to scan
  all page flags to understand the batch topology
- Reordering pages required updating multiple rows (integer shift cascade)
- The `org_*` variants added complexity without a clean separation of concerns

**Fractional indexing (order_key FLOAT8):**
- Initial pages: 1.0, 2.0, 3.0, 4.0
- Insert between page 1 and 2: set order_key = 1.5
- Insert between 1.0 and 1.5: set order_key = 1.25
- **Exactly one row updated per reorder operation**
- `original_page_index` stays immutable — always know where a page started
- Background rebalance job when any gap falls below 0.001

**job_documents table:**
- 3 invoices scanned together = 3 rows in job_documents
- Pages belong to a document via `document_id` FK
- Document reorder = update one integer (`document_index`)
- Bundle grouping = `bundle_name` column on job_documents
- `page_count` maintained automatically by PostgreSQL trigger

---

## ADR-004: Five value layers per field — not one

**Decision:** Store five separate value representations per field, not just the final value.

| Column/Key | Source | Purpose |
|---|---|---|
| `recognition.ocr.raw` | Raw OCR engine output | What the engine literally returned |
| `recognition.ocr.normalized` | Post-processed OCR | After format normalization rules |
| `recognition.llm.raw` | Raw LLM output | What the model literally returned |
| `recognition.llm.normalized` | Post-processed LLM | After normalization |
| `final_value` (relational) | Accepted value | typist_content — what was exported |

**Why:** Discovered by analyzing a real legacy job XML. Example:
```
org_ocr="31/10/25"        ← OCR saw this
ocr_content="20251031"    ← normalized to this
llm_org="20251031"        ← LLM saw this (already normalized in this case)
typist_content="20251031" ← operator accepted this
```
Without all five layers:
- Cannot debug why a field was wrong after the fact
- Cannot retrain models (need raw engine output vs operator correction)
- Cannot detect when OCR and LLM disagree
- Cannot reconstruct the full audit trail for compliance

**Implementation:** Four engine-specific values live in `recognition{}` JSONB
(extensible for new engines). `final_value` is a relational column for query speed.

---

## ADR-005: Two bounding box systems per field

**Decision:** Every field stores coordinates in two separate coordinate systems.

| Key | Coordinate space | Used by |
|---|---|---|
| `geometry.image` | Current scanned page pixels | Verification Workstation (draw highlight box) |
| `geometry.template` | Template definition pixels | Template Editor, geometric registration |

**Why:** Also discovered from real job XML:
```xml
left="306" top="1661" right="475" bottom="1700"          ← image coords
torg_left="704" torg_top="454" torg_right="856" torg_bottom="478"   ← template coords
```
Image coords change with every scan (rotation, skew, scaling vary per document).
Template coords are fixed to the form definition and never change.
Without both: the Verification Workstation cannot correctly position highlight
boxes on screen, and the Template Editor cannot display canonical field positions.

---

## ADR-006: operator_rotation is not scan rotation

**Decision:** `job_pages.operator_rotation` stores only the rotation applied
by a human operator during the Verification Workstation session.

**Source:** `VerifyRotate` property in the Delphi `TJSRPage` class.

**Why it matters:** When a verified job is reopened for review:
- Preprocessing rotation is already baked into the registered TIF file
- operator_rotation needs to be applied on top of that for correct display
- Conflating them would either double-rotate or lose the operator's correction

**Any scan/deskew/preprocessing rotation goes in:** `job_pages.preprocessing{}` JSONB
under a key like `"deskew_angle_deg": 0.8`.

---

## ADR-007: JSONB strategy — what goes where

**Rule:** A column is RELATIONAL if you need to filter, sort, join, or aggregate it
(`WHERE`, `ORDER BY`, `GROUP BY`, `COUNT`). Otherwise JSONB.

### Relational columns (indexed, fast queries)
```
jobs:       status, stage, tenant_id, system_id, priority, on_hold, is_duplicate
            skipped_verify, skipped_trutypist, verifier_name
job_fields: field_state, confidence, value_source, final_value
            field_order, is_visible, is_readonly, setup_table, array_index
```

### JSONB columns (schema evolution, unit storage)
```
jobs.classifications[]      ranked document type candidates (jcn1/jct1/jcc1 pattern)
jobs.job_state{}            pipeline accumulator — each station merges its output
jobs.artifacts[]            MinIO file manifest — appended as each file is written
jobs.pipeline_timings{}     per-station duration in ms — recorded at transition time

job_pages.match_candidates[]   all template match candidates, not just the winner
job_pages.preprocessing{}      image operations applied (deskew angle, enhancement flags)

job_fields.recognition{}    all engine outputs per field (ocr/llm/operator layers)
job_fields.geometry{}       both bounding box coordinate systems as a logical unit
job_fields.setup_info{}     description, field_type, html_id, dictionary reference

job_stats.engine_stats{}    per-engine analytics breakdown
job_stats.validation_results{}  which validation rules fired and their outcomes
job_stats.cross_page_issues[]   fields resolving differently across pages of same job
```

### JSONB update rule — always merge, never replace
```sql
-- CORRECT: merge new keys into existing state
UPDATE jobs SET job_state = job_state || $new_state::jsonb WHERE id = $1

-- WRONG: this erases all previous state
UPDATE jobs SET job_state = $new_state::jsonb WHERE id = $1
```

---

## ADR-008: MinIO for object storage with S3-compatible API

**Decision:** Use MinIO on-prem, native S3/GCS/R2 in cloud. Same SDK call everywhere.

**Why:** An 800-page tax return at 300dpi greyscale TIF produces ~3.2 GB of output.
PostgreSQL is the wrong place for binary data at this scale. The S3-compatible API
means the application has zero knowledge of whether it is talking to MinIO or AWS S3:
```python
s3.put_object(Bucket="jobs", Key=f"{job_id}/pages/001/original.tif", Body=data)
```
Only the endpoint URL changes between deployment tiers.

**Three-tier artifact degradation** (by deployment constraints):
1. Full-page recognition output in object store (default)
2. Per-field candidates only (bandwidth-constrained deployments)
3. Confidence scores only (minimal storage mode)

---

## ADR-009: NATS JetStream over Kafka

**Decision:** Use NATS JetStream as the message bus, not Kafka.

**Why:**
- Single ~20MB Go binary — runs on the same host as everything else for small on-prem
- No JVM, no ZooKeeper, no external coordination service
- Kafka is 30× heavier operationally for no meaningful benefit at this scale
- NATS has equivalent durability (file-backed streams, Raft consensus for clustering)
- Built-in dead-letter via MaxDeliver + consumer groups distribute work automatically
- Microsecond latency vs Kafka's low-millisecond

**Kafka would be justified if:** replay of 6+ months of events across 50+ services,
or sustained throughput above 1 million messages/second sustained.

---

## ADR-010: Temporal.io for workflow orchestration

**Decision:** Use Temporal.io for pipeline orchestration, not a simple retry loop.

**Why:** A document sitting in the Verification queue for 6 hours while an operator
is at lunch is not a "timeout" — it is a valid durable workflow state. Simple retry
loops cannot model this. Temporal holds that state reliably through any infrastructure
event (crash, restart, deploy).

Additional benefits:
- Full workflow history for debugging (complete replay of every step)
- Configurable per-station retry policies with exponential backoff
- Conditional branching: skip Verification if all fields confidence > 0.95
- Dead-letter escalation after N retries
- Self-hostable as a single Docker container for on-prem

---

## ADR-011: Polyglot architecture — language by problem

**Decision:** Use Go, Python, and TypeScript in the same codebase.
Choose language per service based on what the service actually does.

**Go** for: high-throughput I/O, concurrent dispatching, binary portability,
Lambda cold start sensitivity, always-on low-memory services.

**Python** for: ML model training and inference, ONNX serving, OpenCV image processing,
rule engines, data transformation pipelines.

**TypeScript** for: React frontends, services that share types with the frontend,
I/O-bound Lambdas where development speed matters more than raw performance.

**Not accepted as a reason to switch languages:** familiarity, consistency, "just use
one language." Every switch must be justified by the problem characteristics.

---

## ADR-012: Strangler Fig migration from the legacy platform

**Decision:** The legacy system continues running alongside 2.0 during migration.
New modules are introduced progressively, not in a big-bang cutover.

**Phases:**
1. Foundation (schema, storage, messaging infrastructure)
2. Ingestion layer (new system picks up files, runs in parallel)
3. Pipeline modules (shadow mode — compare outputs against 1.0)
4. Human-facing modules (Verification WS replaces Windows client)
5. Native engines (ONNX replaces wrapped legacy DLLs per-engine)

**Legacy engine bridge:** existing Win32 OCR/ICR/OMR DLLs are wrapped behind a
thin REST adapter (Go service). The Recognition Hub calls them via HTTP.
This preserves recognition accuracy during transition while decoupling the architecture.

