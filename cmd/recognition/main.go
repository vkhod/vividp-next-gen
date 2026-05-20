package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"vividp/job"
	"vividp/logger"
	"vividp/recognition"
)

func main() {
	cfg := recognition.LoadConfig()

	log, logCleanup, err := logger.Setup(cfg.LogLevel, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer logCleanup()

	if cfg.AnthropicAPIKey == "" {
		fmt.Fprintln(os.Stderr, "ERROR: ANTHROPIC_API_KEY is required")
		os.Exit(1)
	}
	os.Setenv("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		fmt.Println("\nINFO: shutting down...")
		cancel()
	}()

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		log.Error("ping postgres", "error", err)
		os.Exit(1)
	}
	fmt.Println("✅ PostgreSQL connected")

	nc, err := nats.Connect(cfg.NatsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		log.Error("connect NATS", "error", err)
		os.Exit(1)
	}
	defer nc.Close()
	fmt.Println("✅ NATS connected")

	js, err := jetstream.New(nc)
	if err != nil {
		log.Error("JetStream init", "error", err)
		os.Exit(1)
	}

	store := job.NewStore(db)
	publisher, err := job.NewPublisher(nc)
	if err != nil {
		log.Error("publisher init", "error", err)
		os.Exit(1)
	}
	svc := job.NewService(store, publisher, log)
	fmt.Println("✅ Job Service ready")

	storage, err := recognition.NewStorage(cfg)
	if err != nil {
		log.Error("MinIO storage init", "error", err)
		os.Exit(1)
	}
	fmt.Println("✅ MinIO storage ready")

	for i := 0; i < cfg.WorkerCount; i++ {
		w := recognition.NewWorker(
			fmt.Sprintf("recog-worker-%d", i+1),
			svc, storage, cfg, log,
		)
		go func(worker *recognition.Worker) {
			if err := worker.Start(ctx, js); err != nil && ctx.Err() == nil {
				log.Error("recognition worker error", "error", err)
			}
		}(w)
	}

	fmt.Println()
	fmt.Println("══════════════════════════════════════")
	fmt.Println("VividP Recognition Service running")
	fmt.Printf("  Workers: %d\n", cfg.WorkerCount)
	fmt.Printf("  Default model: %s\n", cfg.DefaultModel)
	fmt.Printf("  Fallback model: %s\n", cfg.FallbackModel)
	fmt.Println("══════════════════════════════════════")

	<-ctx.Done()
	log.Info("recognition service stopped")
}
