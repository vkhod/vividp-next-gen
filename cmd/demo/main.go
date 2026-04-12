// cmd/demo/main.go
//
// Walks a synthetic job through the ingestion pipeline states,
// printing each NATS event as it fires. No real files — pure state machine demo.
//
// Run: go run cmd/demo/main.go
// Requires: docker-compose up -d  (PostgreSQL + NATS must be running)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"formstorm/job"
)

func main() {
	ctx := context.Background()

	// ── Connect ──────────────────────────────────────────────────────────────
	dbURL := getenv("DATABASE_URL", "postgres://formstorm:formstorm_dev@localhost:5432/formstorm")
	db, err := pgxpool.New(ctx, dbURL)
	must("connect postgres", err)
	must("ping postgres", db.Ping(ctx))
	defer db.Close()
	fmt.Println("✅ PostgreSQL connected")

	natsURL := getenv("NATS_URL", "nats://localhost:4222")
	nc, err := nats.Connect(natsURL)
	must("connect nats", err)
	defer nc.Close()
	fmt.Println("✅ NATS connected")

	// ── Wire up Job Service ───────────────────────────────────────────────────
	store := job.NewStore(db)
	publisher, err := job.NewPublisher(nc)
	must("publisher", err)
	svc := job.NewService(store, publisher, slog.Default())
	fmt.Println("✅ Job Service ready")
	fmt.Println()

	// ── Subscribe to all events so we can watch them fire ────────────────────
	js, _ := jetstream.New(nc)
	consumer, err := js.CreateOrUpdateConsumer(ctx, "FORMSTORM_JOBS", jetstream.ConsumerConfig{
		Name:          "demo-watcher",
		FilterSubject: "formstorm.jobs.events.>",
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	must("create consumer", err)

	consumer.Consume(func(msg jetstream.Msg) {
		var event job.Event
		json.Unmarshal(msg.Data(), &event)
		fmt.Printf("   📡 NATS %-26s  job=%.8s  tenant=%s\n",
			"formstorm.jobs.events."+string(event.Status),
			event.JobID, event.TenantID)
	})

	// ── Demo ──────────────────────────────────────────────────────────────────
	banner("DEMO: Ingestion pipeline state walk")

	// ── Step 1: File detected ─────────────────────────────────────────────────
	step(1, "File detected in input bucket → job created")
	j, err := svc.CreateJob(ctx, job.CreateJobRequest{
		TenantID:  "acme-corp",
		SystemID:  "default",
		Filename:  "invoice_B.pdf",
		Bucket:    "input",
		Key:       "tenants/acme-corp/input/invoice_B.pdf",
		SizeBytes: 312400,
		NFiles:    1,
	})
	must("create job", err)
	printJob(j)
	pause()

	// ── Step 2: Ingestion worker claims ───────────────────────────────────────
	step(2, "Ingestion worker claims job → INGESTING")
	j, err = svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngesting,
		NewState: job.StateData{
			"worker_id":  "ingest-worker-1",
			"claimed_at": time.Now().UTC().Format(time.RFC3339),
		},
		WorkerID: "ingest-worker-1",
		Note:     "claimed by ingestion worker",
	})
	must("transition INGESTING", err)
	printJob(j)
	pause()

	// ── Step 3: Conversion starts ─────────────────────────────────────────────
	step(3, "PDF→TIF conversion starts → CONVERTING")
	j, err = svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusConverting,
		NewState: job.StateData{
			"convert_started": time.Now().UTC().Format(time.RFC3339),
			"page_count":      3,
		},
		WorkerID: "ingest-worker-1",
		Note:     "conversion started",
	})
	must("transition CONVERTING", err)
	printJob(j)
	pause()

	// ── Step 4: Ingestion complete ────────────────────────────────────────────
	step(4, "3 TIF pages written to MinIO → INGESTED")
	pageCount := 3
	j.PageCount = &pageCount
	j, err = svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngested,
		NewState: job.StateData{
			"pages_written":  3,
			"ingestion_ms":   420,
			"sha256":         "b7c2d4e8f1a3...",
			"artifact_root":  fmt.Sprintf("jobs/acme-corp/%s/", j.ID),
		},
		WorkerID:    "ingest-worker-1",
		Note:        "ingestion complete — artifacts confirmed in MinIO",
		StationName: "ingest",
		DurationMS:  420,
	})
	must("transition INGESTED", err)
	printJob(j)
	fmt.Println("   ↳ work.classify published — Classification workers will pick this up")
	pause()

	// ── Step 5: Illegal transition attempt ────────────────────────────────────
	step(5, "Attempt illegal transition: INGESTED → COMPLETED (should be rejected)")
	_, err = svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusCompleted,
		NewState: job.StateData{},
		WorkerID: "bad-actor",
		Note:     "trying to skip the pipeline",
	})
	if err != nil {
		fmt.Printf("   ✅ Correctly rejected: %v\n", err)
	} else {
		fmt.Println("   ❌ Should have been rejected — check IsLegalTransition")
	}
	pause()

	// ── Step 6: Inspect final state from PostgreSQL ───────────────────────────
	step(6, "Inspect final job state from PostgreSQL")
	fetched, err := svc.GetJob(ctx, j.ID)
	must("get job", err)

	stateJSON, _ := json.MarshalIndent(fetched.JobState, "      ", "  ")
	fmt.Printf("   job_id   : %s\n", fetched.ID)
	fmt.Printf("   status   : %s\n", fetched.Status)
	fmt.Printf("   tenant   : %s\n", fetched.TenantID)
	fmt.Printf("   system   : %s\n", fetched.SystemID)
	if fetched.PageCount != nil {
		fmt.Printf("   pages    : %d\n", *fetched.PageCount)
	}
	fmt.Printf("   job_state: %s\n", stateJSON)

	// ── Summary ───────────────────────────────────────────────────────────────
	fmt.Println()
	banner("Done")
	fmt.Println("Explore the results:")
	fmt.Println()
	fmt.Println("  PostgreSQL:")
	fmt.Println("    psql postgres://formstorm:formstorm_dev@localhost:5432/formstorm")
	fmt.Println("    SELECT id, status, source_filename, page_count FROM jobs;")
	fmt.Println("    SELECT from_status, to_status, worker_id FROM job_transitions ORDER BY occurred_at;")
	fmt.Println()
	fmt.Println("  NATS monitor:  http://localhost:8222/jsz?streams=1&consumers=1")
	fmt.Println("  MinIO console: http://localhost:9001  (formstorm / formstorm_dev)")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func banner(msg string) {
	line := "─────────────────────────────────────────────────"
	fmt.Println(line)
	fmt.Printf("  %s\n", msg)
	fmt.Println(line)
}

func step(n int, msg string) {
	fmt.Printf("\n→ Step %d: %s\n", n, msg)
}

func printJob(j *job.Job) {
	pages := "?"
	if j.PageCount != nil {
		pages = fmt.Sprintf("%d", *j.PageCount)
	}
	fmt.Printf("   job_id=%.8s  status=%-20s  pages=%s\n", j.ID, j.Status, pages)
}

func pause() {
	time.Sleep(350 * time.Millisecond)
}

func must(label string, err error) {
	if err != nil {
		log.Fatalf("FATAL [%s]: %v", label, err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
