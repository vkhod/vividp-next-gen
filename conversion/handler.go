package conversion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ConvertRequest is the POST /convert body.
type ConvertRequest struct {
	SourceKey    string          `json:"source_key"`
	SourceBucket string          `json:"source_bucket"`
	JobID        string          `json:"job_id"`
	TenantID     string          `json:"tenant_id"`
	SystemID     string          `json:"system_id"`
	TargetFormat string          `json:"target_format"` // "jpeg_pages"
	Options      ConvertOptions  `json:"options"`
}

type ConvertOptions struct {
	DPI     int `json:"dpi"`
	Quality int `json:"quality"`
}

// ConvertResponse is returned on success.
type ConvertResponse struct {
	Pages []PageInfo `json:"pages"`
}

type PageInfo struct {
	Key       string `json:"key"`
	PageNum   int    `json:"page_num"`
	SizeBytes int64  `json:"size_bytes"`
}

// Handler handles POST /convert requests.
type Handler struct {
	storage *Storage
	log     *slog.Logger
}

func NewHandler(storage *Storage, log *slog.Logger) *Handler {
	return &Handler{storage: storage, log: log.With("module", "handler")}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var req ConvertRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.SourceKey == "" || req.JobID == "" {
		http.Error(w, "source_key and job_id are required", http.StatusBadRequest)
		return
	}
	if req.TargetFormat == "" {
		req.TargetFormat = "jpeg_pages"
	}
	if req.SourceBucket == "" {
		req.SourceBucket = h.storage.jobsBucket
	}

	start := time.Now()
	h.log.Info("convert request", "key", req.SourceKey, "format", req.TargetFormat)

	resp, err := h.convert(r.Context(), req)
	if err != nil {
		h.log.Error("conversion failed", "key", req.SourceKey, "error", err)
		http.Error(w, fmt.Sprintf("conversion failed: %v", err), http.StatusInternalServerError)
		return
	}

	h.log.Info("conversion complete", "key", req.SourceKey, "pages", len(resp.Pages), "ms", time.Since(start).Milliseconds())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) convert(ctx context.Context, req ConvertRequest) (*ConvertResponse, error) {
	// Create temp working directory
	workDir, err := os.MkdirTemp("", fmt.Sprintf("vividp-conv-%s-*", req.JobID[:8]))
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Download source file
	ext := filepath.Ext(req.SourceKey)
	srcPath := filepath.Join(workDir, "source"+ext)

	rc, err := h.storage.Download(ctx, req.SourceBucket, req.SourceKey)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer rc.Close()

	f, err := os.Create(srcPath)
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	f.Close()

	// Convert
	outputDir := filepath.Join(workDir, "pages")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	pages, err := ConvertToJPEGPages(ctx, srcPath, outputDir, req.Options.DPI, req.Options.Quality, h.log)
	if err != nil {
		return nil, fmt.Errorf("convert: %w", err)
	}

	// Upload JPEG pages to MinIO
	var pageInfos []PageInfo
	for _, p := range pages {
		key := fmt.Sprintf("jobs/%s/%s/%s/pages/%03d/page.jpg",
			req.TenantID, req.SystemID, req.JobID, p.PageNum)

		size, err := h.storage.UploadJPEG(ctx, key, p.Path)
		if err != nil {
			return nil, fmt.Errorf("upload page %d: %w", p.PageNum, err)
		}
		pageInfos = append(pageInfos, PageInfo{
			Key:       key,
			PageNum:   p.PageNum,
			SizeBytes: size,
		})
	}

	return &ConvertResponse{Pages: pageInfos}, nil
}
