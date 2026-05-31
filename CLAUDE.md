# VividP — Project Context for AI Assistants
---

## Project Identity

| Property | Value |
|---|---|
| **Project name** | VividP |
| **Repo root** | `D:\DEVELOPMENT\VIVIDP-NEXT-GEN` |
| **Go module** | `vividp` |
| **Role** | Complete architectural redesign of the legacy Win32 monolith into a cloud-native platform |

---

## What This Project Is

VividP is a production-grade **Intelligent Document Processing (IDP)** platform.
The existing product is a Win32 monolithic application. This project
rebuilds it as a collection of autonomous, cloud-native, independently deployable modules
that run identically on a single on-premise server or across a global cloud deployment.

**This is a structural transformation, not a feature rewrite.**

---

## Domain Terminology

| Term | Meaning |
|---|---|
| **Job** | Central domain object — one document being processed end-to-end |
| **Job Envelope** | All artifacts + state for a job. Binary files in MinIO/S3, metadata in PostgreSQL |
| **Tenant** | Organizational boundary — one client organization. Strict isolation between tenants |
| **System** | VividP configuration entity — defines forms, templates, fields, rules for a tenant |
| **Station Config** | Per-pipeline-station parameters within a system (ingestion, recognition, verification, export) |
| **Document Type** | A form definition within a system — fields, templates, tables, validation rules |
| **Template** | Pre-defined form layout for structured field extraction (optional for content-first types) |
| **Queue** (legacy) | Legacy term for what is now the Job Envelope |
| **IVO** | Intelligent Verification Oversight module — risk-based attention at verification time |
| **VWD** | Visual Workflow Designer — no-code/low-code workflow builder (deferred) |
| **DMR** | Digital Mail Room — automated inbound document routing |
| **TruTypist** | Operator verification workstation (legacy name) |
| **rfield** | Recognized field element from legacy XML |
| **rpage** | Recognized page element from legacy XML |
| **rpginfo** | Old page batch/bundle topology flags — **replaced by job_documents table** |
| **setup_table** | Line-item table grouping a repeating field belongs to (e.g. "InvoiceLines") |
| **array_index** | 1-based row index for repeating/line-item fields |
| **operator_rotation** | Human-applied rotation during verification (VerifyRotate), distinct from scan rotation |
| **Five value layers** | Per field: OCR raw → OCR normalized → LLM raw → LLM normalized → final/typist |
| **idCliQ** | Identity document reading — explicitly **out of scope** for this modernization |

---

## Technology Stack

| Language | Used For | Why |
|---|---|---|
| **Go** | CPU-bound pipeline/microservices: Ingestion, Job Service, Recognition Hub, API Gateway, Lambda Dispatcher | Concurrency, single binary, fast cold start |
| **Python** | ML/classification, validation logic, data processing | ML ecosystem, ONNX, OpenCV |
| **TypeScript/React** | Human-facing UIs: Verification Workstation, Admin Portal, VWD | React 19, TanStack Query, Tailwind, shadcn/ui |

| Infrastructure | Role |
|---|---|
| **PostgreSQL + JSONB** | Job state, settings, field values, audit log — always running |
| **MinIO / S3** | Binary artifact store — same S3 API on-prem and cloud — always running |
| **NATS JetStream** | Event bus — bucket notifications, job events, config invalidation — always running |
| **Temporal.io** | Durable workflow orchestration |
| **Docker / K8s** | Docker Compose (dev), K3s/K8s (on-prem prod), EKS/GKE (cloud prod) |
| **AWS Lambda** | Stateless processing modules (IVO, classification, validation, DMR) — triggered via lambda-dispatcher |

### Frontend Stack
- React 19 + TypeScript + Vite
- TanStack Query (server state)
- Tailwind CSS + shadcn/ui
- WebSockets for real-time queue updates

---

## Repository Structure

```
vividp-next-gen/
  CLAUDE.md                       ← this file
  docker-compose.yml              ← local dev infrastructure
  docker-compose.prod.yml         ← production stack (all services)
  go.mod                          ← Go module: "vividp"
  db/
    migrations/
      001_settings.sql            ← tenants, systems, station_configs, document_types, ...
      002_jobs.sql                ← jobs, job_documents, job_pages, job_fields, ...
  job/                            ← core domain package (Go)
    model.go                      ← Job, Document, Page, Field structs + state machine
    store.go                      ← PostgreSQL operations (ONLY writer to jobs table)
    publisher.go                  ← NATS JetStream event publishing (publishes vividp.jobs.events.*)
    service.go                    ← business logic orchestrating store + publisher
  ingestion/                      ← ingestion service package (Go) — long-running container
    config.go
    storage.go                    ← MinIO/S3 client wrapper
    subscriber.go                 ← NATS JetStream durable consumer for MinIO events
    accumulator.go                ← FolderAccumulator — in-memory multi-file folder grouping
    worker.go                     ← file processing worker goroutines
  conversion/                     ← conversion service (Go) — long-running container, HTTP server
    handler.go                    ← PDF/image → per-page JPEG via HTTP
    storage.go
    converter.go
  recognition/                    ← recognition service (Go) — long-running container
    worker.go                     ← NATS consumer → Claude API → job_fields
    storage.go
    prompt.go
  export/                         ← export service (Go) — long-running container
    worker.go                     ← NATS consumer → builds result.json → MinIO
    storage.go
  admin/                          ← admin API package (Go)
    store.go                      ← read queries for admin UI
    handler.go                    ← HTTP handlers (read endpoints + /api/internal/* write endpoints for Lambda callbacks)
    config.go
  lambda-dispatcher/              ← NATS → Lambda bridge (Go) — long-running container
    dispatcher.go                 ← subscribes to vividp.jobs.events.*, calls Lambda Function URLs
    config.go                     ← LAMBDA_DISPATCH env var: "STATUS:url,STATUS:url"
  cmd/
    ingestion/main.go             ← Ingestion Service entry point
    conversion/main.go            ← Conversion Service entry point
    recognition/main.go           ← Recognition Service entry point
    export/main.go                ← Export Service entry point
    job-admin-api/main.go         ← Admin API entry point
    lambda-dispatcher/main.go     ← Lambda Dispatcher entry point
  tests/
    integration/
      pipeline_test.go            ← end-to-end tests: state transitions, single-file pipeline, folder pipeline
      helpers_test.go             ← test infrastructure: connect(), waitForStatus(), cleanup helpers
  admin-ui/                       ← Jobs Admin UI (React 19 + Vite + Tailwind + shadcn/ui)
    src/
      pages/jobs/JobsAdminPage.tsx
      components/jobs/            ← JobFilterBar, JobsTable, BulkActionBar, BulkConfirmDialog, StatusBadge
      components/ui/              ← shadcn/ui base components
      api/jobs.ts                 ← fetch wrappers for admin API
      hooks/useJobs.ts            ← TanStack Query wrapper
      types/job.ts                ← Job, JobFilters, JobStatus, BulkAction types
  docs/
    decisions.md                  ← architectural decision log (ADRs)
    schema.md                     ← schema design narrative
    deployment-context.md         ← server, CI/CD, ports, secrets reference
```

---

## Multi-Tenant Settings Architecture

### Entity Hierarchy

```
Platform (hosted defaults)
  └── Tenant (e.g. Bank Hapoalim)
       ├── storage_config, compliance_config, features
       └── System (e.g. HapoalimClassification)
            ├── global_config: processing flags, timers, priority
            ├── hooks: scripting hooks (stored as strings, VWD integration deferred)
            ├── Station Configs (exactly 4 per system)
            │    ├── ingestion: resolution, brightness, page size, priority scheme
            │    ├── recognition: languages, engines, enhancement, full_page mode
            │    ├── verification: RTL, fonts, colors, zoom, highlight behavior
            │    └── export: format, image inclusion, PDF mode
            ├── Exception Types: operator actions per station visibility
            ├── Queues: routing rules, priority config
            └── Document Types (forms)
                 ├── _JobForm: job-level metadata fields (always present)
                 ├── _GlobalForm: fields injected into every document type
                 ├── _Default: fallback for unclassified documents
                 └── Concrete types (e.g. 0101, 0206, 0800)
                      ├── extraction_strategy: content_first | zone_based | hybrid
                      ├── Field Definitions (with recognition, validation, display JSONB)
                      ├── Templates (optional — only for zone_based/hybrid)
                      └── Table Definitions (line-item table structures)
```

### Key Design Decisions

- **Strictly one tenant per system** — no cross-tenant sharing
- **Scripting hooks stored as plain strings** — VWD integration deferred
- **No queue-level config overrides** yet — system-level station configs suffice
- **JSONB for station configs** — heterogeneous per station, always read as full blob, validated by JSON Schema at app layer
- **extraction_strategy on document_types**: `content_first` (default), `zone_based`, `hybrid`
- **Templates are optional** — content_first types have zero rows in templates table. Field definitions (gen_labels, gen_mask, gen_dict) ARE the extraction recipe.
- **Systems are versioned** — system_versions table captures immutable config snapshots for audit/rollback

### Settings Resolution Pattern

```
Service needs config for job J
  → Read J.system_id
  → In-memory cache hit? → Use cached ResolvedSystemConfig (0ms)
  → Cache miss → PostgreSQL: 3 queries (~5ms)
       - systems row
       - station_configs (4 rows)
       - special document types (_JobForm, _GlobalForm, _Default)
  → Cache key: (system_id, version)
  → Invalidation: NATS vividp.systems.config.updated.{system_id}
       All services evict that cache entry. Next job triggers fresh load.
```

Zero database traffic for settings on the hot path after first load.
Document-type-specific field definitions loaded lazily per type and cached separately.

### Field Definition Structure

Each field carries three JSONB config buckets split by consumer:
- **recognition_config** — consumed by Recognition Hub: dictionaries, masks, labels, patterns
- **validation_config** — consumed by Validation Service: functions, date processing, mod10
- **display_config** — consumed by Verification Workstation: RTL, read-only, colors, layout

---

## Database Schema

### Settings tables (001_settings.sql)

| Table | Purpose | Typical size |
|---|---|---|
| `tenants` | Org boundary, storage/compliance config | 1 (on-prem) to ~100 (SaaS) |
| `systems` | Processing personality, global flags, hooks | 1-5 per tenant |
| `system_versions` | Immutable config audit trail | grows over time |
| `station_configs` | Per-station params (JSONB) | exactly 4 per system |
| `document_types` | Form definitions with extraction_strategy | 10-50 per system |
| `field_definitions` | Field extraction recipes (3 JSONB config buckets) | 5-40 per doc type |
| `templates` | Zone coords, optional per extraction_strategy | 0 for content_first |
| `table_definitions` | Line-item table structures | 0-3 per doc type |
| `exception_types` | Operator exception actions | 5-10 per system |
| `queues` | Routing/priority | 1-5 per system |

### Job tables (002_jobs.sql)

| Table | Purpose |
|---|---|
| `jobs` | Central entity — one row per document job. FK to tenants + systems |
| `job_documents` | Logical document grouping within a batch (replaces rpginfo) |
| `job_pages` | Per-page: fractional ordering, operator rotation, preprocessing |
| `job_fields` | Per-field: five value layers, two bbox systems, recognition JSONB |
| `job_transitions` | Immutable audit log of every state change |
| `job_stats` | Computed once at job completion — throughput, correction rates |

### Important: migration file ordering

001_settings.sql runs BEFORE 002_jobs.sql because jobs FK-references tenants and systems.

---

## Local Infrastructure (docker-compose.yml)

| Service | Port | Purpose |
|---|---|---|
| PostgreSQL | 5432 | All tables (settings + jobs) |
| NATS | 4222 | Event bus — client connections |
| NATS | 8222 | Monitoring UI → http://localhost:8222 |
| MinIO | 9000 | S3 API — buckets: input, jobs, templates, archive |
| MinIO | 9001 | Web console → http://localhost:9001 |

### MinIO → NATS bucket notifications

MinIO is configured to publish ObjectCreated events to NATS JetStream natively.
When a file lands in the `input` bucket, NATS receives it on `vividp.minio.events`.
The injection service subscribes to this subject — no polling, no webhooks.

Filtered to relevant file types: `.pdf, .tif, .tiff, .png, .jpg, .jpeg`.

### Dev seed data

001_settings.sql seeds a default tenant (`slug: dev`) and system (`code: default`)
with four station configs for local development:
- Tenant UUID: `00000000-0000-0000-0000-000000000001`
- System UUID: `00000000-0000-0000-0000-000000000002`

### Reset everything

```bash
docker compose down -v
docker compose up -d
```

---

## NATS Subject Conventions

| Subject | Purpose |
|---|---|
| `vividp.minio.events` | MinIO bucket notifications (ObjectCreated) |
| `vividp.jobs.events.{STATUS}` | Job state change broadcasts (fan-out) |
| `vividp.jobs.work.{station}` | Task assignment to worker pools (competing consumers) |
| `vividp.systems.config.updated.{system_id}` | Settings cache invalidation |

---

## Architectural Rules — DO NOT VIOLATE

1. **Job Service as sole writer** — no service writes directly to the `jobs` table
   except through the `job.Service` type.

2. **Five value layers per field** — `ocr_content`, `ocr_original`, `llm_original`,
   `llm_content`, `typist_content` all matter. Do not collapse them.

3. **Two bounding box systems** — image coords (per-scan) and template coords (per-form).
   They are different. Never conflate them.

4. **operator_rotation ≠ scan rotation** — `job_pages.operator_rotation` is VerifyRotate
   from Delphi. Applied by human during verification. Separate from preprocessing rotation.

5. **JSONB merge, never replace** — when updating `job_state`, always use
   `job_state = job_state || $new::jsonb`. Never do a full replacement.

6. **Settings are read-through-cache, never embedded** — pipeline services resolve settings
   from the in-memory cache keyed on (system_id, version). Structural decisions (which engine,
   which template matched) are recorded in the Job Envelope. Behavioral config (UI colors,
   export format) is resolved at runtime.

7. **Templates are optional** — document types with `extraction_strategy = 'content_first'`
   have no template images. The field recognition_config (labels, masks, dictionaries)
   IS the extraction recipe.

8. **Tenant isolation is absolute** — one tenant per system. No cross-tenant data access.
   Storage paths, NATS subjects, and database queries are always scoped by tenant.

9. **Lambda modules never touch PostgreSQL directly** — PostgreSQL is bound to `127.0.0.1` and
   is not reachable from Lambda. All DB writes from Lambda go through
   `job-admin-api /api/internal/*`. Direct MinIO access for binary artifacts is allowed.

10. **lambda-dispatcher is config-only** — adding a new Lambda trigger requires only a new
    `LAMBDA_DISPATCH` env var entry. No code changes to the dispatcher or any pipeline service.

---

## Legacy Compatibility Notes

The schema was designed by analyzing real legacy artifacts:
- HapoalimClassification.xml (system configuration — 30+ form definitions)
- Production job XML files
- `fsjob_json.pas` Delphi source unit

Key mappings:

| Legacy | VividP |
|---|---|
| `<system>` XML root (~40 attributes) | `systems.global_config` JSONB |
| `<parameters>/<input\|ocr\|verify\|export>` | `station_configs` table (4 rows per system) |
| `<form>` elements | `document_types` table |
| `<field>` elements (30+ attributes) | `field_definitions` with 3 JSONB config buckets |
| `<template>` elements | `templates` table (optional for content_first) |
| `<exception>` elements | `exception_types` table |
| `_JobForm` / `_GlobalForm` / `_Default` | Boolean flags on `document_types` |
| `gen_labels`, `gen_mask`, `gen_dict` | `field_definitions.recognition_config` JSONB |
| `validation_func`, `on_next`, `on_change` | `field_definitions.validation_config` JSONB (as strings for now) |
| `display_if`, `read_only`, `eol_before` | `field_definitions.display_config` JSONB |
| `v_name`, `v_time`, `nksv` | `verifier_name`, `verification_seconds`, `keystrokes_count` |
| `jcn1/jct1/jcc1` | `classifications[]` JSONB array |
| `rpginfo` end_doc/bundle flags | `job_documents` table |
| `ARJob.JobFileName` / `ARJob.JobName` | `jobs.job_name` / `jobs.job_alias` |
| `VerifyRotate` | `job_pages.operator_rotation` |
| `bLastInForm` | `job_pages.is_last_in_form` |
| `SetupField.SetupTable.Name` | `job_fields.setup_table` |

---

## Hybrid Lambda Architecture

### Decision

The three core pipeline stages — **ingestion, recognition, export** — stay as long-running Docker containers with NATS JetStream durable consumers. They hold in-memory state (FolderAccumulator), persistent connection pools, and goroutine worker pools that are incompatible with Lambda's stateless, short-lived execution model.

Future processing modules (IVO, classification, validation, DMR, audit hooks) will be implemented as **AWS Lambda functions**. These are stateless, event-triggered, and fit Lambda's model exactly.

PostgreSQL, NATS JetStream, and MinIO always run as containers. They are the permanent backbone that both long-running services and Lambda functions share.

### How Lambda Gets Triggered

Lambda cannot consume NATS natively. A dedicated **`lambda-dispatcher`** service bridges the two:

```
job.Service.Transition() → PublishTransition() → NATS vividp.jobs.events.{STATUS}
                                                         ↓
                                               [lambda-dispatcher container]
                                               subscribes to vividp.jobs.events.*
                                               reads LAMBDA_DISPATCH dispatch table
                                                         ↓
                                               HTTP POST → Lambda Function URL
                                                         ↓
                                               Lambda processes (reads MinIO, calls Claude, etc.)
                                                         ↓
                                               HTTP POST → job-admin-api /api/internal/...
                                               (writes to PG via job.Service, publishes next NATS event)
```

`publisher.go` is unchanged — it already publishes `vividp.jobs.events.*` on every transition. The dispatcher subscribes to those same events.

### Lambda Dispatcher Config

The dispatch table is env-configured — no code change needed when adding a new Lambda:

```
LAMBDA_DISPATCH=RECOGNIZED:https://<id>.lambda-url.eu-west-1.on.aws/ivo,INGESTED:https://.../classify
```

Each entry maps a job status to a Lambda Function URL. The dispatcher sends the `job.Event` JSON payload (job_id, tenant_id, system_id, status, page_count, occurred_at) as the HTTP POST body.

### How Lambda Writes Results Back

PostgreSQL is bound to `127.0.0.1` on the server — Lambda cannot reach it directly. Lambda callbacks go through **job-admin-api internal endpoints**:

```
POST /api/internal/jobs/{id}/transition   — report completion, trigger next pipeline event
POST /api/internal/jobs/{id}/fields       — write recognized/processed fields
```

These endpoints are handled by the existing `job.Service` instance inside job-admin-api. They are not exposed externally by nginx (nginx only proxies `/api/*` that does not match `/api/internal/*`, or they require an internal auth token — to be decided).

MinIO (port 6781) IS reachable externally, so Lambda can read/write artifacts directly without going through a callback.

### What Needs to Be Built

| Component | Location | Status |
|---|---|---|
| Lambda dispatcher service | `lambda-dispatcher/` + `cmd/lambda-dispatcher/` | **Done** |
| Dispatcher Dockerfile + compose entry | `Dockerfile.lambda-dispatcher` | **Done** |
| Internal write endpoints | `admin/handler.go` | **Done** |
| Each Lambda function | separate repo or `lambdas/{name}/` | Per module |

Existing services (`ingestion`, `recognition`, `export`, `job.Service`, `publisher.go`) required **no changes**.

### Architectural Rule for Lambda Modules

> Lambda functions MUST NOT connect directly to PostgreSQL. All DB writes go through `job-admin-api /api/internal/*`. All DB reads that require full job data go through the same API. Direct MinIO access for binary artifacts is allowed.

---

## IVO Module (Intelligent Verification Oversight)

Spec completed. Will be implemented as a **Lambda function** (triggered by `vividp.jobs.events.RECOGNIZED` via the lambda-dispatcher).

Components:
- **Risk Profile Store** — per-field correction rates, cross-field inconsistency patterns
- **Attention Engine** — generates intervention manifests per job (< 200ms)
- **Verifier Telemetry Collector** — captures operator behavior for learning
- **Learning Pipeline** — proposes risk profile updates from telemetry
- **Admin Approval Queue** — human sign-off on learned patterns

Intervention types: `CONFIRM_VALUE`, `RESOLVE_AMBIGUITY`, `CROSS_CHECK_RELATED`, `SUPERVISOR_REVIEW`.

Four open questions remain before implementation begins (see conversation history).

---

## Modules On the Horizon

- **IVO** — Lambda, triggered on RECOGNIZED
- **Classification** — Lambda, triggered on INGESTED (before recognition, if needed)
- **Validation** — Lambda, triggered on RECOGNIZED
- **VWD module spec** — deferred
- **Verification Workstation UI** — complex data-dense tool; custom client scripts via sandboxed JS
- **Legacy engine wrapping** — Win32 OCR/classification behind REST adapters (Strangler Fig)
- **License Service** — new licensing axes: deployment tier, modules, engines, throughput, seats

---

## Build & Fix Strategy

When fixing build errors or making structural changes across multiple files:

1. **Enumerate all affected files first** — before editing any single file, identify every file and every specific location that needs to change. Do not apply partial fixes.
2. **Fix all layers simultaneously** — edit every affected file in one pass. Fixing one layer at a time causes build systems and IDEs to revert or surface new errors on each round.
3. **Verify the full build after every change set** — run `go build ./...` (or the relevant build command) after each set of changes. Do not assume a single-file edit is sufficient.

Primary languages in this codebase: **Go** (pipeline services), **Python** (ML/classification), **TypeScript/React** (UIs). Always confirm end-to-end build success after changes in any of these.

---

## Debugging Philosophy

Before proposing or applying any fix:

1. **Identify the root cause** — do not apply surface-level workarounds (capping values, disabling features, adding fallbacks) unless explicitly asked to. If the root cause is unclear, ask a clarifying question before writing code.
2. **Map all affected layers** — trace the problem through every layer it touches (DB schema → store → service → handler, or config → build → runtime). A fix at one layer that leaves another layer inconsistent is not a fix.
3. **Propose, then implement** — for non-trivial bugs, state the root cause and the proposed fix before making edits. This avoids wasted work when the diagnosis is wrong.

---

## Current Focus

Next up: first production deployment to the on-premise server. Then IVO Lambda.

## Completed Work

### Jobs Admin UI (all phases done)
- [x] Phase 1 — Jobs list view (`admin-ui/`) with filters, bulk actions, multi-select
- [x] Phase 1 amendment — detail panel is a resizable split (drag handle; min 280px, max 800px, default 420px)
- [x] Phase 2 — Job detail view (`admin-ui/src/components/jobs/detail/`) — Event Log + S3 Artifacts panels
- [x] Phase 3 — API wiring (`admin/` package, `cmd/job-admin-api/main.go`, `admin-ui/src/api/jobs.ts`)

### Lambda Dispatcher infrastructure (branch: feature/lambda-dispatcher)
- [x] Lambda dispatcher service — `lambda-dispatcher/` + `cmd/lambda-dispatcher/` + `Dockerfile.lambda-dispatcher`
- [x] Internal Lambda callback endpoints — `POST /api/internal/jobs/{id}/transition` and `.../fields` in `admin/handler.go`
- [x] All pipeline services added to `docker-compose.yml` for local dev (ingestion, recognition, export, job-admin-api)
- [x] Vite dev proxy — `/api` → `localhost:8081` in `admin-ui/vite.config.ts`
- [x] Integration test suite — `tests/integration/` (state transitions, single-file pipeline, folder pipeline)

### Bug fixes (same branch)
- [x] `job_state` JSONB corruption — `json.Marshal(nil)` → `"null"` → `object || 'null'::jsonb` converted column to array; fixed by defaulting to `{}` in `TransitionStatus` when `NewState` is nil
- [x] Claude tool schema — `invoiceToolSchema()` was passing the full schema as `Properties`, nesting it inside itself and causing 400 from API; fixed by splitting into `invoiceToolProperties()` + `invoiceToolRequired`
- [x] `job_transitions` FK — was `NO ACTION`, blocking job deletion; migration 005 changes it to `ON DELETE CASCADE`
- [x] `stage` column — was written once at job creation and never updated; `StageForStatus()` now keeps it in sync on every transition. `FAILED`/`DEAD_LETTER` preserve the last known stage for debugging
- [x] Go 1.25 in all Dockerfiles — was `golang:1.23-alpine`, mismatched `go.mod` requiring 1.25

## Decisions Made

### Admin UI
- Detail view and mini-panels are empty stubs in Phase 1 — built in Phase 2
- Detail panel is a resizable split (drag handle between table and panel; min 280px, max 800px, default 420px)
- Mobile layout is out of scope
- "NATS Messages" panel renamed to "Event Log" and backed by `job_transitions` table (not raw NATS payloads)
- Admin API is a new `cmd/job-admin-api` entry point — does not extend the ingestion service
- No auth/authorization in v1 — network-level access control assumed
- Artifacts panel is lazy — fetches presigned URLs (15-min TTL) only on first expand via `onOpenChange` in CollapsibleSection
- BulkConfirmDialog calls real API per-job with async/error handling; mutations invalidate `['jobs','list']` and open detail cache

### Lambda Dispatcher
- Internal endpoints (`/api/internal/*`) have no token auth — network-level isolation is the only control (nginx does not proxy `/api/internal/*` externally)
- `LAMBDA_DISPATCH` format: `STATUS:url,STATUS:url` — `SplitN(..., 2)` preserves `https://` colons in URLs
- Lambda dispatcher `DeliverPolicy: DeliverNewPolicy` — does not replay historical events on restart, only new ones

### Integration tests
- `TestMain` gates the entire suite on `ANTHROPIC_API_KEY` + reachable infra — skip (not fail) when not available
- `TestJobStore_StateTransitions` is the fast DB-only smoke test (~0.1s); `TestSingleFilePipeline` and `TestFolderJobPipeline` hit the real Claude API (~10–90s each)
- Test fixtures live at `test_input_files/{tenant_id}/{system_id}/`
