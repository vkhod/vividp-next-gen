package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	dispatcher "vividp/lambda-dispatcher"
	"vividp/logger"
)

func main() {
	cfg := dispatcher.LoadConfig()

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

	// Parse dispatch table early — fail fast on misconfiguration
	table, err := cfg.ParseDispatchTable()
	if err != nil {
		log.Error("invalid LAMBDA_DISPATCH", "error", err)
		os.Exit(1)
	}
	if len(table) == 0 {
		log.Warn("LAMBDA_DISPATCH is empty — no Lambdas will be triggered")
	}

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

	d := dispatcher.NewDispatcher(js, table, cfg.HTTPTimeout, log)

	fmt.Println()
	fmt.Println("══════════════════════════════════════")
	fmt.Println("VividP Lambda Dispatcher running")
	fmt.Printf("  Dispatch entries: %d\n", len(table))
	fmt.Println("══════════════════════════════════════")

	if err := d.Run(ctx); err != nil {
		log.Error("dispatcher error", "error", err)
		os.Exit(1)
	}
	log.Info("lambda-dispatcher stopped")
}
