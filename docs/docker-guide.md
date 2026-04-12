# Docker & Docker Compose — FormStorm Infrastructure Guide

> Human reference doc. Not read by AI coding assistants.

---

## What is Docker Compose?

Docker runs individual isolated processes called **containers** — each container is like a mini Linux machine with exactly one service inside (a database, a message broker, etc.).

`docker-compose.yml` is a recipe file that says: "run these containers together, connect them to the same network, and configure them this way." Instead of running 4 separate `docker run` commands with a wall of flags, you run one command:

```bash
docker-compose up -d
```

The `-d` means "detached" — run in the background so your terminal is free.

---

## The four containers in this project

```
docker-compose.yml
├── postgres     → fs_postgres    (the database)
├── nats         → fs_nats        (the message bus)
├── minio        → fs_minio       (the file store)
└── minio_setup  → fs_minio_setup (one-time bucket creator, then exits)
```

### 1. `fs_postgres` — PostgreSQL 16

```yaml
image: postgres:16-alpine
ports: "5432:5432"
```

The database. `16-alpine` means PostgreSQL version 16 on Alpine Linux — a very small base image (~5 MB vs ~500 MB for full Ubuntu).

**The clever part:** the `./db/migrations/` folder is mounted into a special directory that PostgreSQL checks on first start. Any `.sql` file there is run automatically. That's how `001_jobs.sql` creates all 6 tables without manual setup.

```yaml
volumes:
  - postgres_data:/var/lib/postgresql/data        # persistent data survives restarts
  - ./db/migrations:/docker-entrypoint-initdb.d   # auto-runs SQL on first start only
```

The `postgres_data` named volume means data survives `docker-compose restart`. It is only wiped when you run `docker-compose down -v` (the `-v` flag removes volumes).

---

### 2. `fs_nats` — NATS 2.10

```yaml
image: nats:2.10-alpine
ports: "4222:4222"   # client connections
       "8222:8222"   # web monitoring UI
command: "-js -sd /data -m 8222"
```

The message bus — services publish events here and others subscribe. The flags mean:
- `-js` — enable JetStream (NATS's persistent, reliable messaging layer)
- `-sd /data` — store messages on disk so they survive restarts
- `-m 8222` — expose the monitoring web UI on port 8222

Browse the UI at http://localhost:8222 to see streams, consumers, and message counts.

---

### 3. `fs_minio` — MinIO

```yaml
image: minio/minio:latest
ports: "9000:9000"   # S3-compatible API
       "9001:9001"   # web console
command: "server /data --console-address :9001"
```

MinIO is an S3-compatible object store — it stores binary files (PDFs, TIFs). The Go code uses the same API as AWS S3, so swapping MinIO for real S3 in production requires changing only the endpoint URL. Nothing in the application code changes.

Browse the console at http://localhost:9001 (login: `formstorm` / `formstorm_dev`).

---

### 4. `fs_minio_setup` — the bucket creator

```yaml
image: minio/mc:latest
depends_on:
  minio: { condition: service_healthy }
```

`mc` is the MinIO command-line client. This container runs three commands and then exits:

```bash
mc alias set local http://minio:9000 formstorm formstorm_dev
mc mb local/jobs
mc mb local/input
mc mb local/archive
```

Notice it connects to `http://minio:9000` — not `localhost`. Inside the Docker network, containers address each other by **service name** (`minio`, `postgres`, `nats`), not by localhost.

`depends_on: service_healthy` means it waits until MinIO passes its health check before running.

---

## Port mapping

Every container uses `"HOST_PORT:CONTAINER_PORT"` mapping:

```yaml
ports:
  - "5432:5432"   # your machine : inside the container
```

So `localhost:5432` on your Windows machine reaches PostgreSQL inside its container. The Go code connects to `localhost:5432` — it doesn't know or care it's talking to a container.

---

## Health checks

Each service has a health check:

```yaml
healthcheck:
  test: ["CMD", "pg_isready", "-U", "formstorm"]
  interval: 5s
  retries: 5
```

Docker probes the container on this interval. A container is `healthy` only when the check passes. The `minio_setup` container waits for `minio` to be healthy before creating buckets. That's why you won't see a race condition on first start.

---

## The network picture

```
Your Windows machine
├── localhost:5432  ──→  fs_postgres  (PostgreSQL)
├── localhost:4222  ──→  fs_nats      (NATS client)
├── localhost:8222  ──→  fs_nats      (NATS web UI)
├── localhost:9000  ──→  fs_minio     (S3 API)
└── localhost:9001  ──→  fs_minio     (MinIO web console)

Inside the Docker network ("formstorm-next-gen_default"):
├── postgres:5432   ← how containers reach each other
├── nats:4222
└── minio:9000

Go services (running on Windows, NOT in Docker):
└── connect to localhost:* — same as you in a browser
```

The Go services run **outside** Docker during development (`go run ...`). Only the infrastructure is containerised. This keeps the edit/compile/debug loop fast — no rebuilding Docker images when you change Go code.

---

## Key commands

```bash
# Start everything (detached / background)
docker-compose up -d

# See container status and health
docker-compose ps

# Live logs from one service
docker-compose logs -f postgres
docker-compose logs -f nats

# Stop containers (data is preserved)
docker-compose down

# Full reset — stop AND delete all data
docker-compose down -v

# Open an interactive shell inside a container
docker exec -it fs_postgres bash

# Run a one-off command inside a container
docker exec fs_postgres psql -U formstorm -d formstorm -c "SELECT * FROM jobs;"

# Restart a single service without touching others
docker-compose restart postgres
```

---

## Credentials (local dev only)

| Service    | Username     | Password       |
|------------|--------------|----------------|
| PostgreSQL | `formstorm`  | `formstorm_dev` |
| MinIO      | `formstorm`  | `formstorm_dev` |

Connection strings used by the Go services (these are the defaults in `ingestion/config.go`):

```
DATABASE_URL  = postgres://formstorm:formstorm_dev@localhost:5432/formstorm
NATS_URL      = nats://localhost:4222
STORAGE_ENDPOINT = localhost:9000
```

---

## Why not run Go services in Docker too?

During development you don't want to — rebuilding a Docker image every time you save a `.go` file would be slow. The pattern here is:

- **Infrastructure** (PostgreSQL, NATS, MinIO) → Docker. These rarely change, start once, run forever.
- **Application code** (Go services) → run directly on your machine with `go run`. Fast iteration, native debugger support, instant restarts.

When deploying to production, the Go services would also be containerised (each with its own `Dockerfile`). That work is deferred — the focus right now is building the logic.
