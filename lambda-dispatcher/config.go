package dispatcher

import (
	"fmt"
	"os"
	"strings"
	"time"

	"vividp/job"
)

// Config holds all runtime configuration for the lambda-dispatcher service.
type Config struct {
	NatsURL        string
	LambdaDispatch string // raw env: "STATUS:url,STATUS:url"
	HTTPTimeout    time.Duration
	LogLevel       string
	LogFile        string
}

// LoadConfig reads configuration from environment variables with sensible defaults.
func LoadConfig() Config {
	return Config{
		NatsURL:        getenv("NATS_URL", "nats://localhost:4222"),
		LambdaDispatch: getenv("LAMBDA_DISPATCH", ""),
		HTTPTimeout:    parseDuration(getenv("LAMBDA_HTTP_TIMEOUT", "30s")),
		LogLevel:       getenv("LOG_LEVEL", "info"),
		LogFile:        getenv("LOG_FILE", ""),
	}
}

// ParseDispatchTable parses the LAMBDA_DISPATCH env var into a status→URL map.
// Format: "RECOGNIZED:https://fn-url/ivo,INGESTED:https://fn-url/classify"
// Uses SplitN(..., 2) so the URL's own colons (https:) are preserved.
func (c Config) ParseDispatchTable() (map[job.Status]string, error) {
	table := make(map[job.Status]string)
	if c.LambdaDispatch == "" {
		return table, nil
	}
	for _, entry := range strings.Split(c.LambdaDispatch, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid LAMBDA_DISPATCH entry %q: expected STATUS:url", entry)
		}
		status := job.Status(strings.TrimSpace(parts[0]))
		url := strings.TrimSpace(parts[1])
		if url == "" {
			return nil, fmt.Errorf("empty URL for status %q in LAMBDA_DISPATCH", status)
		}
		table[status] = url
	}
	return table, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
