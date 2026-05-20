package conversion

import "os"

type Config struct {
	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageSecure    bool
	JobsBucket       string

	ListenAddr string
	LogLevel   string
}

func LoadConfig() Config {
	return Config{
		StorageEndpoint:  getenv("STORAGE_ENDPOINT", "localhost:9000"),
		StorageAccessKey: getenv("STORAGE_ACCESS_KEY", "vividp"),
		StorageSecretKey: getenv("STORAGE_SECRET_KEY", "vividp_dev"),
		StorageSecure:    getenv("STORAGE_SECURE", "false") == "true",
		JobsBucket:       getenv("JOBS_BUCKET", "jobs"),
		ListenAddr:       getenv("LISTEN_ADDR", ":8081"),
		LogLevel:         getenv("LOG_LEVEL", "info"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
