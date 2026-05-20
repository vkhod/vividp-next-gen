package export

import "os"

type Config struct {
	DatabaseURL string
	NatsURL     string

	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageSecure    bool
	JobsBucket       string

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
