// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/config.go
// Configuration loaded from environment variables.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import "os"

// Config holds all ingestion service configuration.
// Every field has an environment variable source and a sensible default.
type Config struct {
	// Database
	DatabaseURL string

	// NATS
	NatsURL string

	// MinIO / S3
	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageSecure    bool
	InputBucket      string
	JobsBucket       string

	// Service identity
	DefaultSystemID  string // UUID of the VividP System to assign new jobs
	DefaultTenantID  string // UUID fallback tenant if not extractable from key

	// HTTP server
	WebhookPort   string
	WebhookSecret string // optional shared secret for webhook validation

	// Worker pool
	WorkerCount   int
	WorkQueueSize int

	// Logging
	LogLevel string // env: LOG_LEVEL, default "info"
	LogFile  string // env: LOG_FILE,  default "logs/ingestion.log" (empty = stdout only)
}

func LoadConfig() Config {
	return Config{
		DatabaseURL:      getenv("DATABASE_URL",      "postgres://vividp:vividp_dev@localhost:5432/vividp"),
		NatsURL:          getenv("NATS_URL",           "nats://localhost:4222"),
		StorageEndpoint:  getenv("STORAGE_ENDPOINT",  "localhost:9000"),
		StorageAccessKey: getenv("STORAGE_ACCESS_KEY","vividp"),
		StorageSecretKey: getenv("STORAGE_SECRET_KEY","vividp_dev"),
		StorageSecure:    getenv("STORAGE_SECURE",    "false") == "true",
		InputBucket:      getenv("INPUT_BUCKET",      "input"),
		JobsBucket:       getenv("JOBS_BUCKET",       "jobs"),
		DefaultSystemID:  getenv("VIVIDP_DEFAULT_SYSTEM_ID",  "00000000-0000-0000-0000-000000000002"),
		DefaultTenantID:  getenv("VIVIDP_DEFAULT_TENANT_ID",  "00000000-0000-0000-0000-000000000001"),
		WebhookPort:      getenv("WEBHOOK_PORT",       "8080"),
		WebhookSecret:    getenv("WEBHOOK_SECRET",     ""),
		WorkerCount:   2, // increase for production
		WorkQueueSize: 100,
		LogLevel:      getenv("LOG_LEVEL", "info"),
		LogFile:       getenv("LOG_FILE", "logs/ingestion.log"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}