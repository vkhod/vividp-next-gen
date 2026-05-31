package admin

import "os"

// Config holds all admin API configuration, loaded from environment variables.
type Config struct {
	DatabaseURL      string
	NatsURL          string
	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageSecure    bool
	JobsBucket       string
	ListenAddr       string
	LogLevel         string
	LogFile          string

	// Auth — required when ADMIN_PASSWORD is set; skipped if empty (dev only).
	AdminUsername   string // env: ADMIN_USERNAME, default "admin"
	AdminPassword   string // env: ADMIN_PASSWORD
	SessionSecret   string // env: ADMIN_SESSION_SECRET
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
		ListenAddr:       getenv("ADMIN_API_ADDR", ":8081"),
		LogLevel:         getenv("LOG_LEVEL", "info"),
		LogFile:          getenv("LOG_FILE", "logs/job-admin-api.log"),
		AdminUsername:    getenv("ADMIN_USERNAME", "admin"),
		AdminPassword:    getenv("ADMIN_PASSWORD", ""),
		SessionSecret:    getenv("ADMIN_SESSION_SECRET", ""),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
