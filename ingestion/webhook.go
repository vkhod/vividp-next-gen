// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/webhook.go
// HTTP handler for MinIO ObjectCreated webhook events.
// MinIO fires this when a file lands in the /input bucket.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// S3Event is the webhook payload MinIO sends on ObjectCreated.
// Matches the Amazon S3 event notification schema.
type S3Event struct {
	EventName string     `json:"EventName"`
	Key       string     `json:"Key"`
	Records   []S3Record `json:"Records"`
}

type S3Record struct {
	EventName string `json:"eventName"`
	S3        struct {
		Bucket struct {
			Name string `json:"name"`
		} `json:"bucket"`
		Object struct {
			Key  string `json:"key"`
			Size int64  `json:"size"`
			ETag string `json:"eTag"`
		} `json:"object"`
	} `json:"s3"`
}

// DetectedFile holds the parsed information from a webhook event.
type DetectedFile struct {
	Bucket   string
	Key      string
	Filename string
	Size     int64
	TenantID string
	SystemID string // populated from path or config default

	// Folder mode (Phase 4) — zero values mean single-file job
	IsFolder    bool
	AllKeys     []FileEntry
	MetaContent string
}

// FileEntry represents a single file within a folder-mode job.
type FileEntry struct {
	Key      string
	Filename string
	Size     int64
}

// WebhookHandler handles incoming MinIO webhook events.
type WebhookHandler struct {
	workQueue       chan<- DetectedFile
	secret          string // optional: shared webhook secret for validation
	defaultTenantID string // UUID fallback when tenant not in key path
	defaultSystemID string // UUID fallback when system not in key path
	accumulator     *FolderAccumulator
	storage         *Storage
	log             *slog.Logger
}

func NewWebhookHandler(q chan<- DetectedFile, secret, defaultTenantID, defaultSystemID string, acc *FolderAccumulator, storage *Storage, log *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		workQueue:       q,
		secret:          secret,
		defaultTenantID: defaultTenantID,
		defaultSystemID: defaultSystemID,
		accumulator:     acc,
		storage:         storage,
		log:             log.With("module", "webhook"),
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Optional webhook secret validation
	if h.secret != "" && r.Header.Get("X-Webhook-Secret") != h.secret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var event S3Event
	if err := json.Unmarshal(body, &event); err != nil {
		// Try single-record format
		var record S3Record
		if err2 := json.Unmarshal(body, &record); err2 != nil {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}
		event.Records = []S3Record{record}
	}

	dispatched := 0
	for _, rec := range event.Records {
		if !strings.Contains(rec.EventName, "ObjectCreated") {
			continue
		}

		key, _ := url.PathUnescape(rec.S3.Object.Key)
		prefix := folderPrefix(key)

		// Signal file — triggers release of accumulated folder as one job.
		// Always skip as a document regardless of path depth.
		if isSignalFile(key) {
			if prefix != "" && h.accumulator != nil {
				tenantID, systemID := extractRouting(key, h.defaultTenantID, h.defaultSystemID)
				bucket := rec.S3.Bucket.Name
				var metaContent string
				if filepath.Base(key) == "_READY.json" {
					if rc, err := h.storage.ReadObject(r.Context(), bucket, key); err == nil {
						if raw, err := io.ReadAll(io.LimitReader(rc, 1<<16)); err == nil {
							metaContent = string(raw)
						}
						rc.Close()
					}
				}
				detected, ok := h.accumulator.Signal(prefix, filepath.Base(prefix), metaContent)
				if ok {
					detected.Bucket = bucket
					detected.TenantID = tenantID
					detected.SystemID = systemID
					select {
					case h.workQueue <- detected:
						dispatched++
						h.log.Info("folder job queued", "folder", detected.Filename, "file_count", len(detected.AllKeys))
					default:
						http.Error(w, "queue full", http.StatusServiceUnavailable)
						return
					}
				}
			}
			continue
		}

		if shouldIgnore(key) {
			continue
		}

		// Folder member — accumulate until signal arrives
		if prefix != "" && h.accumulator != nil {
			tenantID, systemID := extractRouting(key, h.defaultTenantID, h.defaultSystemID)
			h.accumulator.Add(prefix, DetectedFile{
				Bucket:   rec.S3.Bucket.Name,
				Key:      key,
				Filename: filepath.Base(key),
				Size:     rec.S3.Object.Size,
				TenantID: tenantID,
				SystemID: systemID,
			})
			continue
		}

		// Single file — queue directly
		tenantID, systemID := extractRouting(key, h.defaultTenantID, h.defaultSystemID)
		detected := DetectedFile{
			Bucket:   rec.S3.Bucket.Name,
			Key:      key,
			Filename: filepath.Base(key),
			Size:     rec.S3.Object.Size,
			TenantID: tenantID,
			SystemID: systemID,
		}
		select {
		case h.workQueue <- detected:
			dispatched++
			h.log.Info("file queued", "file", detected.Filename, "tenant", detected.TenantID, "size", detected.Size)
		default:
			h.log.Warn("work queue full — dropping file", "key", key)
			http.Error(w, "queue full", http.StatusServiceUnavailable)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":%d}`, dispatched)
}

// extractRouting parses tenant and system IDs from the object key.
// Supports two formats:
//   - tenants/{tenant}/input/{file}           → uses defaultSystemID
//   - tenants/{tenant}/{system}/input/{file}  → uses system from path
func extractRouting(key, defaultTenantID, defaultSystemID string) (tenantID, systemID string) {
	parts := strings.Split(key, "/")
	if len(parts) < 4 || parts[0] != "tenants" {
		return defaultTenantID, defaultSystemID
	}
	if parts[2] == "input" {
		return parts[1], defaultSystemID
	}
	if len(parts) >= 5 && parts[3] == "input" {
		return parts[1], parts[2]
	}
	return parts[1], defaultSystemID
}

// isSignalFile reports whether the key's base name is _READY or _READY.json.
func isSignalFile(key string) bool {
	base := filepath.Base(key)
	return base == "_READY" || base == "_READY.json"
}

// folderPrefix returns the path up to and including the subfolder name,
// e.g. "tenants/t/s/input/batch-001/file.pdf" → "tenants/t/s/input/batch-001".
// Returns "" if the file sits directly under input/ (not in a subfolder).
func folderPrefix(key string) string {
	parts := strings.Split(key, "/")
	// Need at least: tenants / tenant / system / input / folder / file  (6 parts)
	if len(parts) < 6 {
		return ""
	}
	// Find "input" segment — folder must be the segment after it
	for i, p := range parts {
		if p == "input" && i+2 < len(parts) {
			return strings.Join(parts[:i+2], "/")
		}
	}
	return ""
}

// shouldIgnore returns true for keys that are not real documents.
func shouldIgnore(key string) bool {
	base := strings.ToLower(filepath.Base(key))
	// Ignore hidden files, temp files, and non-document formats
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".tmp") {
		return true
	}
	return false
}
