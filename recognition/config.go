package recognition

import "os"

type Config struct {
	DatabaseURL string
	NatsURL     string

	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageSecure    bool
	JobsBucket       string

	AnthropicAPIKey  string
	DefaultModel     string // default: claude-haiku-4-5
	FallbackModel    string // used when confidence is low: claude-sonnet-4-6

	WorkerCount int
	LogLevel    string
}

func LoadConfig() Config {
	return Config{
		DatabaseURL:      getenv("DATABASE_URL", "postgres://vividp:vividp_dev@localhost:5432/vividp"),
		NatsURL:          getenv("NATS_URL", "nats://localhost:4222"),
		StorageEndpoint:  getenv("STORAGE_ENDPOINT", "localhost:9000"),
		StorageAccessKey: getenv("STORAGE_ACCESS_KEY", "vividp"),
		StorageSecretKey: getenv("STORAGE_SECRET_KEY", "vividp_dev"),
		StorageSecure:    getenv("STORAGE_SECURE", "false") == "true",
		JobsBucket:       getenv("JOBS_BUCKET", "jobs"),
		AnthropicAPIKey:  getenv("ANTHROPIC_API_KEY", ""),
		DefaultModel:     getenv("RECOGNITION_MODEL", "claude-haiku-4-5"),
		FallbackModel:    getenv("RECOGNITION_FALLBACK_MODEL", "claude-sonnet-4-6"),
		WorkerCount:      2,
		LogLevel:         getenv("LOG_LEVEL", "info"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
