package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vividp/conversion"
	"vividp/logger"
)

func main() {
	cfg := conversion.LoadConfig()

	log, logCleanup, err := logger.Setup(cfg.LogLevel, "")
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

	storage, err := conversion.NewStorage(cfg)
	if err != nil {
		log.Error("MinIO storage init failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("✅ MinIO storage ready")

	handler := conversion.NewHandler(storage, log)

	mux := http.NewServeMux()
	mux.Handle("POST /convert", handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Minute, // conversion can take time
		WriteTimeout: 5 * time.Minute,
	}

	go func() {
		fmt.Printf("✅ Conversion service listening on %s\n", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	fmt.Println()
	fmt.Println("══════════════════════════════════════")
	fmt.Println("VividP Conversion Service running")
	fmt.Printf("  Health:   http://localhost%s/healthz\n", cfg.ListenAddr)
	fmt.Printf("  Convert:  POST http://localhost%s/convert\n", cfg.ListenAddr)
	fmt.Println("══════════════════════════════════════")

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	log.Info("conversion service stopped")
}
