package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"vividp/ingestion"
	"vividp/job"
	"vividp/license"
	"vividp/logger"
)

func main() {
	cfg := ingestion.LoadConfig()

	// ── Structured logger ────────────────────────────────────────────────────
	log, logCleanup, err := logger.Setup(cfg.LogLevel, cfg.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer logCleanup()

	// ── Context with graceful shutdown ───────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT / SIGTERM
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		fmt.Println("\nINFO: shutting down...")
		cancel()
	}()

	// ── Connect to PostgreSQL ────────────────────────────────────────────────
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err = db.Ping(ctx); err != nil {
		log.Error("ping postgres failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("✅ PostgreSQL connected")

	// ── Connect to NATS ──────────────────────────────────────────────────────
	nc, err := nats.Connect(cfg.NatsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		log.Error("connect NATS failed", "error", err)
		os.Exit(1)
	}
	defer nc.Close()
	fmt.Println("✅ NATS connected")

	js, err := jetstream.New(nc)
	if err != nil {
		log.Error("JetStream init failed", "error", err)
		os.Exit(1)
	}

	// ── Wire up Job Service ──────────────────────────────────────────────────
	store := job.NewStore(db)
	publisher, err := job.NewPublisher(nc)
	if err != nil {
		log.Error("publisher init failed", "error", err)
		os.Exit(1)
	}
	svc := job.NewService(store, publisher, log)
	fmt.Println("✅ Job Service ready")

	// ── Wire up Storage ──────────────────────────────────────────────────────
	storage, err := ingestion.NewStorage(cfg)
	if err != nil {
		log.Error("MinIO storage init failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("✅ MinIO storage ready")

	// ── Folder accumulator (PostgreSQL-backed) ──────────────────────────────
	acc := ingestion.NewFolderAccumulator(db, log)

	// ── Conversion service client ────────────────────────────────────────────
	converter := ingestion.NewConversionClient(cfg.ConversionURL)
	fmt.Printf("✅ Conversion client → %s\n", cfg.ConversionURL)

	// ── Work queue — buffered channel between webhook and workers ────────────
	workQueue := make(chan ingestion.DetectedFile, cfg.WorkQueueSize)

	// ── Start worker pool ────────────────────────────────────────────────────
	lic := license.AlwaysGranted{}
	for i := 0; i < cfg.WorkerCount; i++ {
		w := ingestion.NewWorker(
			fmt.Sprintf("ingest-worker-%d", i+1),
			svc, storage, converter, cfg, lic, log,
		)
		go w.Run(ctx, workQueue)
	}
	fmt.Printf("✅ %d ingestion workers started\n", cfg.WorkerCount)

	// ── Startup reconciliation ───────────────────────────────────────────────
	reconciler := ingestion.NewReconciler(svc, storage, cfg, acc, log)
	reconciler.Run(ctx, workQueue)
	fmt.Println("✅ Startup reconciliation complete")

	// ── NATS subscriber — consumes MinIO bucket notifications ────────────────
	sub := ingestion.NewSubscriber(js, workQueue, cfg, acc, storage, log)
	go func() {
		if err := sub.Start(ctx); err != nil && ctx.Err() == nil {
			log.Error("subscriber error", "error", err)
		}
	}()

	// ── HTTP webhook server ──────────────────────────────────────────────────
	handler := ingestion.NewWebhookHandler(workQueue, cfg.WebhookSecret,
		cfg.DefaultTenantID, cfg.DefaultSystemID, acc, storage, log)

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	server := &http.Server{
		Addr:         ":" + cfg.WebhookPort,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		fmt.Printf("✅ Webhook server listening on :%s\n", cfg.WebhookPort)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Println("VividP Ingestion Service running")
	fmt.Printf("  Webhook:    http://localhost:%s/webhook\n", cfg.WebhookPort)
	fmt.Printf("  Health:     http://localhost:%s/healthz\n", cfg.WebhookPort)
	fmt.Printf("  Workers:    %d\n", cfg.WorkerCount)
	fmt.Printf("  System:     %s\n", cfg.DefaultSystemID)
	fmt.Printf("  Input bucket: %s → Jobs bucket: %s\n", cfg.InputBucket, cfg.JobsBucket)
	fmt.Printf("  Log file:   %s\n", cfg.LogFile)
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("To test: drop a PDF into MinIO /input bucket, or POST a webhook:")
	fmt.Printf("  curl -s -X POST http://localhost:%s/webhook \\\n", cfg.WebhookPort)
	fmt.Println(`       -H 'Content-Type: application/json' \`)
	fmt.Println(`       -d '{"Records":[{"eventName":"s3:ObjectCreated:Put","s3":{"bucket":{"name":"input"},"object":{"key":"tenants/acme-corp/input/test.pdf","size":12345}}}]}'`)

	// ── Block until shutdown ─────────────────────────────────────────────────
	<-ctx.Done()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	server.Shutdown(shutCtx)

	// Drain the work queue gracefully
	close(workQueue)
	log.Info("ingestion service stopped")
}

// ════════════════════════════════════════════════════════════════════════════════
// SETUP & RUNNING GUIDE
// ════════════════════════════════════════════════════════════════════════════════
//
// Prerequisites:
//   docker-compose up -d          (PostgreSQL, NATS, MinIO)
//   go mod tidy                   (downloads all dependencies)
//
// Configure MinIO webhook (one-time setup):
//   1. Open http://localhost:9001 (vividp / vividp_dev)
//   2. Go to Buckets → input → Events
//   3. Add event: POST http://host.docker.internal:8080/webhook
//      Events: PUT (ObjectCreated)
//      Suffix: (empty = all files)
//
// Run the ingestion service:
//   go run cmd/ingestion/main.go
//
// Log output:
//   Operational events are written as NDJSON to both stdout and logs/ingestion.log.
//   Docker/K8s mode (stdout only): LOG_FILE="" go run cmd/ingestion/main.go
//   Filter warnings (PowerShell): Get-Content logs/ingestion.log | Where-Object { $_ -match '"level":"WARN"' }
