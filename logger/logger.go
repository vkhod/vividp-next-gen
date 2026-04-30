// ═══════════════════════════════════════════════════════════════════════════════
// logger/logger.go
// Shared structured logger for all VividP services.
// Produces NDJSON (one JSON object per line) — compatible with Docker/K8s log
// collectors (Loki, Datadog, CloudWatch) out of the box.
// ═══════════════════════════════════════════════════════════════════════════════
package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Setup creates a JSON slog.Logger that writes to stdout and optionally to a file.
//
// level:    "debug"|"info"|"warn"|"error" — anything else defaults to "info".
// filePath: path to log file on disk. Empty string = stdout only (Docker/K8s mode).
//
// Returns the logger and a cleanup func that must be deferred to close the log file.
//
// Usage:
//
//	log, cleanup, err := logger.Setup(cfg.LogLevel, cfg.LogFile)
//	if err != nil { ... }
//	defer cleanup()
func Setup(level, filePath string) (*slog.Logger, func(), error) {
	var w io.Writer = os.Stdout
	cleanup := func() {}

	if filePath != "" {
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, nil, err
		}
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, nil, err
		}
		w = io.MultiWriter(os.Stdout, f)
		cleanup = func() { f.Close() }
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(handler), cleanup, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
