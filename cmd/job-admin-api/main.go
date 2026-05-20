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

	"vividp/admin"
	"vividp/job"
	"vividp/logger"
)

func main() {
	cfg := admin.LoadConfig()

	log, logCleanup, err := logger.Setup(cfg.LogLevel, cfg.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer logCleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		fmt.Println("\nINFO: shutting down...")
		cancel()
	}()

	// ── PostgreSQL ───────────────────────────────────────────────────────────
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

	// ── NATS (needed by job.Service for hold/release publish) ────────────────
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

	// ── Job Service ──────────────────────────────────────────────────────────
	store := job.NewStore(db)
	publisher, err := job.NewPublisher(nc)
	if err != nil {
		log.Error("publisher init", "error", err)
		os.Exit(1)
	}
	svc := job.NewService(store, publisher, log)
	fmt.Println("✅ Job Service ready")

	// ── Admin handler ────────────────────────────────────────────────────────
	adminStore := admin.NewStore(db)
	handler, err := admin.NewHandler(adminStore, svc, cfg, log)
	if err != nil {
		log.Error("admin handler init", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      admin.CORSMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		fmt.Printf("✅ Admin API listening on %s\n", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	fmt.Println()
	fmt.Println("══════════════════════════════════════")
	fmt.Println("VividP Job Admin API running")
	fmt.Printf("  Health: http://localhost%s/healthz\n", cfg.ListenAddr)
	fmt.Printf("  Jobs:   http://localhost%s/api/admin/jobs\n", cfg.ListenAddr)
	fmt.Println("══════════════════════════════════════")
	fmt.Println()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("admin API stopped")
}
