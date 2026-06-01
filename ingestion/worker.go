// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/worker.go
// Processes detected files: create job → archive original → convert → ingest.
// Runs as a pool of goroutines consuming from the work queue.
// Conversion is delegated to the conversion service over HTTP.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vividp/job"
	"vividp/license"
)

// Worker processes one file at a time from the work queue.
type Worker struct {
	id        string
	svc       *job.Service
	storage   *Storage
	converter *ConversionClient
	cfg       Config
	lic       license.Checker
	log       *slog.Logger
}

func NewWorker(id string, svc *job.Service, storage *Storage, converter *ConversionClient, cfg Config, lic license.Checker, log *slog.Logger) *Worker {
	return &Worker{
		id:        id,
		svc:       svc,
		storage:   storage,
		converter: converter,
		cfg:       cfg,
		lic:       lic,
		log:       log.With("module", "worker", "worker_id", id),
	}
}

// Run processes files from the queue until the context is cancelled.
func (w *Worker) Run(ctx context.Context, queue <-chan DetectedFile) {
	w.log.Info("worker started")
	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker stopping")
			return
		case f, ok := <-queue:
			if !ok {
				return
			}
			if err := w.processFile(ctx, f); err != nil {
				w.log.Error("worker failed", "file", f.Filename, "error", err)
			}
		}
	}
}

func (w *Worker) processFile(ctx context.Context, f DetectedFile) error {
	if f.IsFolder {
		return w.processFolderJob(ctx, f)
	}
	return w.processSingleFile(ctx, f)
}

// processSingleFile is the full ingestion flow for one file.
func (w *Worker) processSingleFile(ctx context.Context, f DetectedFile) error {
	if !w.lic.CanIngest(f.TenantID, f.SystemID) {
		w.log.Warn("license denied — dropping file", "tenant", f.TenantID, "file", f.Filename)
		return nil
	}

	start := time.Now()
	w.log.Info("processing file", "file", f.Filename, "tenant", f.TenantID)

	// ── Step 1: Create job ───────────────────────────────────────────────────
	j, err := w.svc.CreateJob(ctx, job.CreateJobRequest{
		TenantID:  f.TenantID,
		SystemID:  f.SystemID,
		Filename:  f.Filename,
		Bucket:    f.Bucket,
		Key:       f.Key,
		SizeBytes: f.Size,
	})
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	w.log.Info("job created", "job_id", j.ID, "file", f.Filename)

	// ── Step 1b: Apply optional .meta sidecar ─────────────────────────────
	w.applyMeta(ctx, j.ID, f.Bucket, f.Key+".meta")

	// ── Step 2: Transition to INGESTING ─────────────────────────────────────
	j, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngesting,
		NewState: job.StateData{
			"worker_id":  w.id,
			"claimed_at": time.Now().UTC().Format(time.RFC3339),
		},
		WorkerID: w.id,
		Note:     "claimed by ingestion worker",
	})
	if err != nil {
		return fmt.Errorf("transition to INGESTING: %w", err)
	}

	// ── Step 3: Download source file → archive to jobs bucket ───────────────
	workDir, err := os.MkdirTemp("", fmt.Sprintf("fs-ingest-%s-*", j.ID[:8]))
	if err != nil {
		return w.failJob(ctx, j.ID, "create temp dir", err)
	}
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, f.Filename)
	if err := w.storage.DownloadToTemp(ctx, f.Bucket, f.Key, srcPath); err != nil {
		return w.failJob(ctx, j.ID, "download source file", err)
	}

	origKey, origSize, err := w.storage.UploadOriginal(ctx, j.ID, f.TenantID, f.SystemID, srcPath)
	if err != nil {
		return w.failJob(ctx, j.ID, "archive original", err)
	}

	w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
		JobID: j.ID,
		Artifact: job.Artifact{
			Key:       origKey,
			Type:      "original",
			MimeType:  mimeTypeFromFilename(f.Filename),
			PageNum:   0,
			SizeBytes: origSize,
			CreatedAt: time.Now().UTC(),
		},
	})

	// ── Step 4: Transition to CONVERTING → call conversion service ───────────
	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusConverting,
		NewState: job.StateData{"convert_started": time.Now().UTC().Format(time.RFC3339)},
		WorkerID: w.id,
		Note:     "conversion started",
	})
	if err != nil {
		return w.failJob(ctx, j.ID, "transition to CONVERTING", err)
	}

	convResult, err := w.converter.Convert(ctx, j.ID, f.TenantID, f.SystemID, origKey)
	if err != nil {
		return w.failJob(ctx, j.ID, "conversion service", err)
	}

	w.log.Info("conversion complete", "job_id", j.ID, "file", f.Filename, "pages", len(convResult.Pages))

	// ── Step 5: Create document + page records ───────────────────────────────
	doc := &job.Document{
		JobID:                 j.ID,
		DocumentIndex:         0,
		OriginalDocumentIndex: 0,
	}
	if err := w.svc.CreateDocument(ctx, doc); err != nil {
		w.log.Warn("failed to create document record", "job_id", j.ID, "error", err)
	}

	for _, pg := range convResult.Pages {
		w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
			JobID: j.ID,
			Artifact: job.Artifact{
				Key:       pg.Key,
				Type:      "page_jpeg",
				MimeType:  "image/jpeg",
				PageNum:   pg.PageNum,
				SizeBytes: pg.SizeBytes,
				CreatedAt: time.Now().UTC(),
			},
		})

		docID := doc.ID
		state := "ingested"
		srcPageNum := pg.PageNum
		page := &job.Page{
			JobID:             j.ID,
			DocumentID:        &docID,
			OriginalPageIndex: pg.PageNum - 1,
			SourcePageNumber:  &srcPageNum,
			OrderKey:          float64(pg.PageNum),
			State:             &state,
		}
		if err := w.svc.CreatePage(ctx, page); err != nil {
			w.log.Warn("failed to create page record", "job_id", j.ID, "page", pg.PageNum, "error", err)
		}
	}

	// ── Step 6: Finalize ─────────────────────────────────────────────────────
	pageCount := len(convResult.Pages)
	if err := w.svc.SetPageCount(ctx, j.ID, pageCount); err != nil {
		w.log.Warn("set page count failed", "job_id", j.ID, "error", err)
	}

	duration := time.Since(start).Milliseconds()
	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngested,
		NewState: job.StateData{
			"page_count":    pageCount,
			"pages_written": pageCount,
			"ingestion_ms":  duration,
		},
		WorkerID:    w.id,
		Note:        "ingestion complete",
		StationName: "ingest",
		DurationMS:  duration,
	})
	if err != nil {
		return fmt.Errorf("transition to INGESTED: %w", err)
	}

	w.log.Info("file ingested", "job_id", j.ID, "file", f.Filename, "pages", pageCount, "ms", duration)
	return nil
}

// processFolderJob ingests all files in a folder as one multi-document job.
func (w *Worker) processFolderJob(ctx context.Context, f DetectedFile) error {
	if !w.lic.CanIngest(f.TenantID, f.SystemID) {
		w.log.Warn("license denied — dropping folder", "tenant", f.TenantID, "folder", f.Filename)
		return nil
	}

	start := time.Now()
	w.log.Info("processing folder", "folder", f.Filename, "file_count", len(f.AllKeys), "tenant", f.TenantID)

	var totalSize int64
	for _, fe := range f.AllKeys {
		totalSize += fe.Size
	}

	// ── Step 1: Create job ───────────────────────────────────────────────────
	j, err := w.svc.CreateJob(ctx, job.CreateJobRequest{
		TenantID:  f.TenantID,
		SystemID:  f.SystemID,
		Filename:  f.Filename,
		Bucket:    f.Bucket,
		Key:       f.Key,
		SizeBytes: totalSize,
	})
	if err != nil {
		return fmt.Errorf("create folder job: %w", err)
	}
	w.log.Info("folder job created", "job_id", j.ID, "folder", f.Filename)

	// ── Step 1b: Apply meta from _READY.json ─────────────────────────────────
	if f.MetaContent != "" {
		if payload, err := parseMeta([]byte(f.MetaContent)); err != nil {
			w.log.Warn("could not parse _READY.json meta — skipping", "job_id", j.ID, "error", err)
		} else if payload != nil {
			w.applyMetaPayload(ctx, j.ID, payload)
		}
	}

	// ── Step 2: Transition to INGESTING ─────────────────────────────────────
	j, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngesting,
		NewState: job.StateData{"worker_id": w.id, "claimed_at": time.Now().UTC().Format(time.RFC3339)},
		WorkerID: w.id,
		Note:     "folder job claimed by ingestion worker",
	})
	if err != nil {
		return fmt.Errorf("transition folder job to INGESTING: %w", err)
	}

	// ── Step 3: Transition to CONVERTING ────────────────────────────────────
	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusConverting,
		NewState: job.StateData{"convert_started": time.Now().UTC().Format(time.RFC3339)},
		WorkerID: w.id,
		Note:     "folder conversion started",
	})
	if err != nil {
		return w.failJob(ctx, j.ID, "transition folder job to CONVERTING", err)
	}

	workDir, err := os.MkdirTemp("", fmt.Sprintf("fs-ingest-%s-*", j.ID[:8]))
	if err != nil {
		return w.failJob(ctx, j.ID, "create temp dir", err)
	}
	defer os.RemoveAll(workDir)

	// ── Step 4: Process each file ────────────────────────────────────────────
	globalPageNum := 0
	totalPages := 0

	for docIdx, fe := range f.AllKeys {
		srcPath := filepath.Join(workDir, fmt.Sprintf("doc%02d_%s", docIdx, fe.Filename))

		if err := w.storage.DownloadToTemp(ctx, f.Bucket, fe.Key, srcPath); err != nil {
			return w.failJob(ctx, j.ID, fmt.Sprintf("download doc %d", docIdx), err)
		}

		origKey, origSize, err := w.storage.UploadOriginal(ctx, j.ID, f.TenantID, f.SystemID, srcPath)
		if err != nil {
			return w.failJob(ctx, j.ID, fmt.Sprintf("archive doc %d", docIdx), err)
		}
		w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
			JobID: j.ID,
			Artifact: job.Artifact{
				Key:       origKey,
				Type:      "original",
				MimeType:  mimeTypeFromFilename(fe.Filename),
				PageNum:   0,
				SizeBytes: origSize,
				CreatedAt: time.Now().UTC(),
			},
		})

		convResult, err := w.converter.Convert(ctx, j.ID, f.TenantID, f.SystemID, origKey)
		if err != nil {
			return w.failJob(ctx, j.ID, fmt.Sprintf("convert doc %d", docIdx), err)
		}

		doc := &job.Document{
			JobID:                 j.ID,
			DocumentIndex:         docIdx,
			OriginalDocumentIndex: docIdx,
		}
		if err := w.svc.CreateDocument(ctx, doc); err != nil {
			w.log.Warn("failed to create document record", "job_id", j.ID, "doc_index", docIdx, "error", err)
		}

		for _, pg := range convResult.Pages {
			globalPageNum++
			w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
				JobID: j.ID,
				Artifact: job.Artifact{
					Key:       pg.Key,
					Type:      "page_jpeg",
					MimeType:  "image/jpeg",
					PageNum:   globalPageNum,
					SizeBytes: pg.SizeBytes,
					CreatedAt: time.Now().UTC(),
				},
			})

			docID := doc.ID
			state := "ingested"
			srcPageNum := pg.PageNum
			page := &job.Page{
				JobID:             j.ID,
				DocumentID:        &docID,
				OriginalPageIndex: globalPageNum - 1,
				SourcePageNumber:  &srcPageNum,
				OrderKey:          float64(globalPageNum),
				State:             &state,
			}
			if err := w.svc.CreatePage(ctx, page); err != nil {
				w.log.Warn("failed to create page record", "job_id", j.ID, "page", globalPageNum, "error", err)
			}
		}
		totalPages += len(convResult.Pages)
	}

	// ── Step 5: Finalize ─────────────────────────────────────────────────────
	if err := w.svc.SetPageCount(ctx, j.ID, totalPages); err != nil {
		w.log.Warn("set page count failed", "job_id", j.ID, "error", err)
	}

	duration := time.Since(start).Milliseconds()
	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngested,
		NewState: job.StateData{
			"page_count":   totalPages,
			"doc_count":    len(f.AllKeys),
			"folder_mode":  true,
			"ingestion_ms": duration,
		},
		WorkerID:    w.id,
		Note:        "folder ingestion complete",
		StationName: "ingest",
		DurationMS:  duration,
	})
	if err != nil {
		return fmt.Errorf("transition folder job to INGESTED: %w", err)
	}

	w.log.Info("folder ingested", "job_id", j.ID, "folder", f.Filename,
		"docs", len(f.AllKeys), "pages", totalPages, "ms", duration)
	return nil
}

func (w *Worker) failJob(ctx context.Context, jobID, step string, cause error) error {
	msg := fmt.Sprintf("%s: %v", step, cause)
	w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    jobID,
		ToStatus: job.StatusFailed,
		NewState: job.StateData{"error": msg},
		WorkerID: w.id,
		Note:     msg,
	})
	return fmt.Errorf("ingestion failed at [%s]: %w", step, cause)
}

func (w *Worker) applyMeta(ctx context.Context, jobID, bucket, metaKey string) {
	if _, err := w.storage.StatObject(ctx, bucket, metaKey); err != nil {
		return
	}
	rc, err := w.storage.ReadObject(ctx, bucket, metaKey)
	if err != nil {
		w.log.Warn("could not read meta sidecar", "job_id", jobID, "key", metaKey, "error", err)
		return
	}
	defer rc.Close()
	data, _ := io.ReadAll(io.LimitReader(rc, 1<<16))

	w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
		JobID: jobID,
		Artifact: job.Artifact{
			Key:       metaKey,
			Type:      "meta_json",
			MimeType:  "application/json",
			SizeBytes: int64(len(data)),
			CreatedAt: time.Now().UTC(),
		},
	})

	payload, err := parseMeta(data)
	if err != nil {
		w.log.Warn("could not parse meta sidecar — skipping", "job_id", jobID, "key", metaKey, "error", err)
		return
	}
	if payload != nil {
		w.applyMetaPayload(ctx, jobID, payload)
	}
}

// applyMetaPayload writes parsed meta to the correct job columns and JSONB state.
func (w *Worker) applyMetaPayload(ctx context.Context, jobID string, payload *MetaPayload) {
	if payload.JobAlias != nil {
		if err := w.svc.SetJobAlias(ctx, jobID, *payload.JobAlias); err != nil {
			w.log.Warn("could not set job_alias", "job_id", jobID, "error", err)
		}
	}
	state := job.StateData{}
	if payload.Priority != nil {
		state["priority"] = int(*payload.Priority)
	}
	if payload.UserData != nil {
		state["user_data"] = *payload.UserData
	}
	if len(payload.CustomFields) > 0 {
		state["meta_fields"] = payload.CustomFields
	}
	if len(state) > 0 {
		w.svc.MergeJobState(ctx, jobID, state)
	}
}

// ProcessFile is the exported entry point used by integration tests to drive
// the ingestion pipeline directly without going through the NATS subscriber.
func (w *Worker) ProcessFile(ctx context.Context, f DetectedFile) error {
	return w.processFile(ctx, f)
}

// mimeTypeFromFilename returns a MIME type based on file extension.
func mimeTypeFromFilename(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".bmp":
		return "image/bmp"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
