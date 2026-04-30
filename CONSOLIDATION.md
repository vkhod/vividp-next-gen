# VividP — Pre-Injection Consolidation

## What changed and why

### docker-compose.yml

**New: MinIO → NATS bucket notifications**
MinIO now publishes `ObjectCreated` events to NATS JetStream natively.
When a file lands in the `input` bucket, MinIO fires an event to
`vividp.minio.events`. The injection service subscribes to this
subject — no polling, no filesystem watchers, no webhooks to manage.

Config is via MinIO environment variables:
- `MINIO_NOTIFY_NATS_ENABLE_PRIMARY: "on"`
- `MINIO_NOTIFY_NATS_ADDRESS_PRIMARY: "nats:4222"`
- `MINIO_NOTIFY_NATS_SUBJECT_PRIMARY: "vividp.minio.events"`
- `MINIO_NOTIFY_NATS_JETSTREAM_PRIMARY: "on"` (durable — survives restarts)

MinIO now `depends_on` NATS (must be healthy before MinIO starts, since
MinIO needs NATS to register the notification target).

**New bucket: `templates`**
For template images (zone_based extraction strategy). Used by the
Recognition Hub when a document type uses zone-based matching.
Content-first document types don't need this bucket at all.

**Notification filter**
Only fires on relevant file types: `.pdf, .tif, .tiff, .png, .jpg, .jpeg`.
Prevents spurious events from temp files or metadata writes.

---

### 002_settings.sql (new file)

Full multi-tenant settings schema. Tables:

| Table | Purpose | Rows (typical) |
|---|---|---|
| `tenants` | Org boundary, storage/compliance config | 1 (on-prem) to ~100 (SaaS) |
| `systems` | Processing personality, global flags | 1-5 per tenant |
| `system_versions` | Immutable config audit trail | grows over time |
| `station_configs` | Per-station params (JSONB) | exactly 4 per system |
| `document_types` | Form definitions | 10-50 per system |
| `field_definitions` | Field extraction recipes | 5-40 per document type |
| `templates` | Zone coords (optional) | 0 for content_first, N for zone_based |
| `table_definitions` | Line-item table structures | 0-3 per document type |
| `exception_types` | Operator exception actions | 5-10 per system |
| `queues` | Routing/priority | 1-5 per system |

Key design decisions:
- **extraction_strategy** on `document_types`: `content_first` (default), `zone_based`, or `hybrid`
- **JSONB** for all heterogeneous config (station params, recognition, validation, display)
- **Hooks stored as plain strings** — VWD integration deferred
- **No queue-level config overrides** yet — system-level station configs are sufficient
- **Strictly one tenant per system** — no cross-tenant sharing
- **Dev seed data** — creates a default tenant + system + 4 station configs for local development

---

### 001_jobs.sql — changes needed

The existing `jobs` table has these columns as TEXT:

```sql
tenant_id       TEXT        NOT NULL,
system_id       TEXT        NOT NULL DEFAULT 'default',
```

Since we're in planning phase (no migration constraints), update them to
proper UUID foreign keys:

```sql
tenant_id       UUID        NOT NULL REFERENCES tenants(id),
system_id       UUID        NOT NULL REFERENCES systems(id),
```

**Important**: 002_settings.sql must run BEFORE 001_jobs.sql because
`jobs` now references `tenants` and `systems`. Rename the files:

```
001_settings.sql   (was 002_settings.sql — tenants, systems, ...)
002_jobs.sql       (was 001_jobs.sql — jobs, job_documents, ...)
```

This ensures PostgreSQL `initdb.d` runs them in the right order:
tenants/systems created first, then jobs can reference them.

Also update the seed data in 002_jobs.sql to reference the well-known
dev tenant/system UUIDs:

```sql
-- In the dev seed INSERT for jobs (if any):
tenant_id = '00000000-0000-0000-0000-000000000001',  -- dev tenant
system_id = '00000000-0000-0000-0000-000000000002',  -- default system
```

---

## File layout after consolidation

```
vividp-next-gen/
├── docker-compose.yml                    # Updated
├── job-service/
│   └── db/
│       └── migrations/
│           ├── 001_settings.sql          # Tenants, systems, document types
│           └── 002_jobs.sql              # Jobs, pages, fields, transitions
├── docs/
│   ├── decisions.md                      # ADRs
│   └── schema.md                         # Schema narrative
└── ...
```

---

## Infrastructure after `docker compose up`

| Service | Port | Purpose |
|---|---|---|
| PostgreSQL | 5432 | Job state + settings (all tables) |
| NATS | 4222 | Event bus (client), receives MinIO notifications |
| NATS | 8222 | Monitoring UI → http://localhost:8222 |
| MinIO | 9000 | S3 API (input, jobs, templates, archive buckets) |
| MinIO | 9001 | Web console → http://localhost:9001 |

**Data flow for injection (ready after this setup):**

```
File dropped → MinIO input bucket
                 ↓
         MinIO fires ObjectCreated event
                 ↓
         NATS: vividp.minio.events
                 ↓
         Injection Service (subscribes)
                 ↓
         Resolves tenant + system from path
         Loads ingestion station_config
         Creates job row in PostgreSQL
         Publishes vividp.jobs.events.detected
```

---

## Settings resolution at runtime (recap)

```
Service needs config for job J
         ↓
Read J.system_id
         ↓
In-memory cache hit? → Use cached ResolvedSystemConfig (0ms)
         ↓ miss
PostgreSQL: 3 queries (~5ms)
  - systems WHERE id = X
  - station_configs WHERE system_id = X
  - document_types WHERE system_id = X AND (is_global OR is_default OR is_job_form)
         ↓
Cache populated, key = (system_id, version)
         ↓
Invalidation: NATS vividp.systems.config.updated.{system_id}
  → all services evict that cache entry
```
