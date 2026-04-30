# VividP — Next Generation IDP Platform

VividP is a production-grade **Intelligent Document Processing (IDP)** platform, rebuilt from a legacy Win32 monolith into a collection of autonomous, cloud-native, independently deployable services. The system runs identically on a single on-premise server or across a global cloud deployment.

---

## Architecture Overview

```
                   ┌─────────────┐
         Upload    │    MinIO    │  ObjectCreated
         Files ──► │  (S3 API)  │ ──────────────► NATS JetStream
                   └─────────────┘                      │
                                                         ▼
                                              ┌─────────────────────┐
                                              │  Ingestion Service  │
                                              │  - Subscriber       │
                                              │  - Worker Pool      │
                                              │  - PDF → TIF Conv.  │
                                              └──────────┬──────────┘
                                                         │
                                              ┌──────────▼──────────┐
                                              │     PostgreSQL      │
                                              │  jobs + settings    │
                                              └─────────────────────┘
```

Documents flow from MinIO → NATS → Ingestion → PostgreSQL. Each document becomes a **Job**, which travels through the pipeline: `DETECTED → INGESTING → CONVERTING → INGESTED → ... → EXPORTED`.

---

## Technology Stack

| Layer | Technology | Purpose |
|---|---|---|
| **Go** | 1.22 | Ingestion, Job Service, API Gateway — concurrency, single binary |
| **Python** | TBD | ML classification, validation, ONNX/OpenCV |
| **TypeScript/React** | TBD | Verification Workstation, Admin Portal |
| **PostgreSQL** | 16 | Job state, settings, field values, audit log |
| **MinIO** | Latest | Binary artifact store — S3-compatible, on-prem and cloud |
| **NATS JetStream** | Latest | Event bus — bucket notifications, job events, config invalidation |
| **Temporal.io** | TBD | Durable workflow orchestration |
| **Docker / K8s** | — | Docker Compose (dev), K3s (on-prem), EKS/GKE (cloud) |

---

## Repository Structure

```
vividp-next-gen/
  cmd/
    ingestion/main.go         ← Ingestion Service entry point
    demo/main.go              ← Local demo / smoke test
  db/
    migrations/
      001_settings.sql        ← Tenants, systems, station configs, document types, fields
      002_jobs.sql            ← Jobs, documents, pages, fields, transitions, stats
  docs/
    decisions.md              ← Architectural Decision Records (ADRs)
    schema.md                 ← Schema design narrative
    docker-guide.md           ← Docker Compose setup guide
  ingestion/                  ← Ingestion service package
    config.go                 ← Environment-based configuration
    storage.go                ← MinIO/S3 client wrapper
    converter.go              ← PDF/image → per-page TIF conversion
    subscriber.go             ← NATS JetStream consumer for MinIO events
    webhook.go                ← MinIO ObjectCreated webhook HTTP handler
    worker.go                 ← File processing worker goroutines
    reconciler.go             ← Startup scan — recovers missed files
    accumulator.go            ← Folder-mode job buffering
  job/                        ← Core domain package
    model.go                  ← Job, Document, Page, Field structs + state machine
    store.go                  ← PostgreSQL operations (sole writer to jobs table)
    publisher.go              ← NATS JetStream event publishing
    service.go                ← Business logic orchestrating store + publisher
  logger/
    logger.go                 ← Structured JSON logger (log/slog)
  docker-compose.yml          ← Local dev infrastructure
  go.mod                      ← Go module: "vividp"
  CLAUDE.md                   ← AI assistant project context
```

---

## Local Development Setup

### Prerequisites

- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [Go 1.22+](https://go.dev/dl/)
- [Ghostscript](https://ghostscript.com/releases/gsdnld.html) — PDF → TIF conversion (`gs --version` to verify)

### 1. Start infrastructure

```bash
docker compose up -d
```

This starts:

| Service | Port | URL |
|---|---|---|
| PostgreSQL | 5432 | — |
| NATS | 4222 / 8222 | http://localhost:8222 (monitor) |
| MinIO | 9000 / 9001 | http://localhost:9001 (console) |

MinIO credentials: `minioadmin / minioadmin`
PostgreSQL: `vividp / vividp_dev` → database `vividp`

### 2. Run database migrations

Migrations run automatically on first `docker compose up` via the init script. To reset everything:

```bash
docker compose down -v
docker compose up -d
```

### 3. Run the Ingestion Service

```bash
go run cmd/ingestion/main.go
```

Or build and run:

```bash
go build -o bin/ingestion cmd/ingestion/main.go
./bin/ingestion
```

### 4. Configuration (environment variables)

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://vividp:vividp_dev@localhost:5432/vividp` | PostgreSQL connection string |
| `NATS_URL` | `nats://localhost:4222` | NATS server |
| `MINIO_ENDPOINT` | `localhost:9000` | MinIO endpoint |
| `MINIO_ACCESS_KEY` | `minioadmin` | MinIO access key |
| `MINIO_SECRET_KEY` | `minioadmin` | MinIO secret key |
| `INPUT_BUCKET` | `input` | Bucket to watch for new files |
| `DEFAULT_TENANT_ID` | `00000000-0000-0000-0000-000000000001` | Fallback tenant UUID |
| `DEFAULT_SYSTEM_ID` | `00000000-0000-0000-0000-000000000002` | Fallback system UUID |
| `WORKER_COUNT` | `4` | Number of parallel ingestion workers |

---

## Ingesting Documents

Drop any supported file into the MinIO `input` bucket. MinIO fires an ObjectCreated event to NATS, the subscriber picks it up, and a worker converts it to a per-page TIF and records the job in PostgreSQL.

**Supported formats:** `.pdf`, `.tif`, `.tiff`, `.png`, `.jpg`, `.jpeg`

**Single file:** drop directly into `input/`

**Folder batch (multi-document job):** create a subfolder, add files, then upload a `_READY.json` signal file to trigger ingestion as one job. The `_READY.json` can carry optional metadata:

```json
{
  "job_alias": "Invoice batch April",
  "priority": 5,
  "user_data": "any string"
}
```

**Folder key structure (for routing):**
```
tenants/{tenant-uuid}/input/{filename}
tenants/{tenant-uuid}/{system-uuid}/input/{folder}/{file}
```
Files not matching this pattern are assigned to the default tenant/system.

---

## Domain Model

### Job lifecycle

```
DETECTED → INGESTING → CONVERTING → INGESTED → [pipeline stations] → EXPORTED
                                                       ↓
                                                    FAILED
```

Every state transition is recorded in `job_transitions` — a complete, immutable audit log.

### Key tables

| Table | Purpose |
|---|---|
| `tenants` | Organizational boundary — strict isolation |
| `systems` | Processing personality — forms, engines, timers |
| `station_configs` | Per-station JSONB config (ingestion / recognition / verification / export) |
| `document_types` | Form definitions with extraction strategy |
| `field_definitions` | Field recipes split across three JSONB buckets (recognition / validation / display) |
| `jobs` | Central entity — one row per document job |
| `job_documents` | Logical document groupings within a batch |
| `job_pages` | Per-page: ordering, rotation, preprocessing |
| `job_fields` | Per-field: five value layers (OCR raw/normalized, LLM raw/normalized, typist) |
| `job_transitions` | Immutable state change audit log |
| `job_stats` | Computed at completion — throughput, correction rates, engine breakdown |

---

## Architectural Rules

1. **Job Service is the sole writer** to the `jobs` table — no service bypasses `job.Service`.
2. **Five value layers per field** — `ocr_original`, `ocr_content`, `llm_original`, `llm_content`, `typist_content` — never collapsed.
3. **Two bounding box systems** — image coords (per-scan) and template coords (per-form) — never conflated.
4. **JSONB merge, never replace** — `job_state = job_state || $new::jsonb` always.
5. **Settings are read-through-cache** — zero DB traffic on the hot path after first load; invalidated via NATS.
6. **Tenant isolation is absolute** — storage paths, NATS subjects, and queries are always tenant-scoped.

---

## NATS Subject Conventions

| Subject | Purpose |
|---|---|
| `vividp.minio.events` | MinIO bucket notifications |
| `vividp.jobs.events.{STATUS}` | Job state change broadcasts |
| `vividp.jobs.work.{station}` | Task assignment to worker pools |
| `vividp.systems.config.updated.{system_id}` | Settings cache invalidation |

---

## Modules Status

| Module | Status |
|---|---|
| Ingestion Service | In progress |
| Job Service | In progress |
| Recognition Hub | Planned |
| Validation Service | Planned |
| Verification Workstation (UI) | Planned |
| Admin Portal (UI) | Planned |
| Export Service | Planned |
| IVO (Intelligent Verification Oversight) | Spec complete |
| VWD (Visual Workflow Designer) | Deferred |
