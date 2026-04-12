// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/worker.go
// Processes detected files: create job → convert → write artifacts → transition.
// Runs as a pool of goroutines consuming from the work queue.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"formstorm/job"
	"formstorm/license"
)

// Worker processes one file at a time from the work queue.
type Worker struct {
	id      string
	svc     *job.Service
	storage *Storage
	cfg     Config
	lic     license.Checker
	log     *slog.Logger
}

func NewWorker(id string, svc *job.Service, storage *Storage, cfg Config, lic license.Checker, log *slog.Logger) *Worker {
	return &Worker{
		id:      id,
		svc:     svc,
		storage: storage,
		cfg:     cfg,
		lic:     lic,
		log:     log.With("module", "worker", "worker_id", id),
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

	// ── Step 1: Create job immediately ──────────────────────────────────────
	// Job exists in the DB from this moment — survives any downstream crash.
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

	// ── Step 3: Set up temp working directory ───────────────────────────────
	workDir, err := os.MkdirTemp("", fmt.Sprintf("fs-ingest-%s-*", j.ID[:8]))
	if err != nil {
		return w.failJob(ctx, j.ID, "create temp dir", err)
	}
	defer os.RemoveAll(workDir) // always clean up

	// ── Step 4: Download source file from MinIO ─────────────────────────────
	srcPath := filepath.Join(workDir, f.Filename)
	if err := w.storage.DownloadToTemp(ctx, f.Bucket, f.Key, srcPath); err != nil {
		return w.failJob(ctx, j.ID, "download source file", err)
	}

	// ── Step 5: Archive original file to jobs bucket ─────────────────────────
	origKey, origSize, err := w.storage.UploadOriginal(ctx, j.ID, f.TenantID, f.SystemID, srcPath)
	if err != nil {
		return w.failJob(ctx, j.ID, "archive original", err)
	}

	// Record original file artifact
	w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
		JobID: j.ID,
		Artifact: job.Artifact{
			Key:       origKey,
			Type:      "original_pdf",
			PageNum:   0,
			SizeBytes: origSize,
			CreatedAt: time.Now().UTC(),
		},
	})

	// ── Step 6: Convert to TIF pages ─────────────────────────────────────────
	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusConverting,
		NewState: job.StateData{
			"convert_started": time.Now().UTC().Format(time.RFC3339),
		},
		WorkerID: w.id,
		Note:     "conversion started",
	})
	if err != nil {
		return w.failJob(ctx, j.ID, "transition to CONVERTING", err)
	}

	outputDir := filepath.Join(workDir, "pages")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return w.failJob(ctx, j.ID, "create output dir", err)
	}

	convResult, err := ConvertToTIF(ctx, srcPath, outputDir, w.log)
	if err != nil {
		return w.failJob(ctx, j.ID, "convert to TIF", err)
	}

	w.log.Info("conversion complete", "job_id", j.ID, "file", f.Filename, "pages", convResult.PageCount)

	// ── Step 7: Upload TIF pages to MinIO ────────────────────────────────────
	// Create one document grouping for the whole file
	doc := &job.Document{
		JobID:                 j.ID,
		DocumentIndex:         0,
		OriginalDocumentIndex: 0,
	}
	if err := w.svc.CreateDocument(ctx, doc); err != nil {
		w.log.Warn("failed to create document record", "job_id", j.ID, "error", err)
		// Non-fatal — continue with page creation
	}

	for i, pagePath := range convResult.PagePaths {
		pageNum := i + 1

		// Upload TIF to MinIO
		tifKey, tifSize, err := w.storage.UploadTIF(ctx, j.ID, f.TenantID, f.SystemID, pageNum, pagePath)
		if err != nil {
			return w.failJob(ctx, j.ID, fmt.Sprintf("upload page %d", pageNum), err)
		}

		// Record artifact
		w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
			JobID: j.ID,
			Artifact: job.Artifact{
				Key:       tifKey,
				Type:      "original_tif",
				PageNum:   pageNum,
				SizeBytes: tifSize,
				CreatedAt: time.Now().UTC(),
			},
		})

		// Create page record (order_key = float64(pageNum))
		docID := doc.ID
		state := "ingested"
		srcPageNum := pageNum
		page := &job.Page{
			JobID:             j.ID,
			DocumentID:        &docID,
			OriginalPageIndex: i, // 0-based
			SourcePageNumber:  &srcPageNum,
			OrderKey:          float64(pageNum),
			State:             &state,
		}
		if err := w.svc.CreatePage(ctx, page); err != nil {
			w.log.Warn("failed to create page record", "job_id", j.ID, "page", pageNum, "error", err)
			// Non-fatal — continue
		}
	}

	// ── Step 8: Update page count and transition to INGESTED ─────────────────
	if err := w.svc.SetPageCount(ctx, j.ID, convResult.PageCount); err != nil {
		w.log.Warn("set page count failed", "job_id", j.ID, "error", err)
	}

	duration := time.Since(start).Milliseconds()

	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusIngested,
		NewState: job.StateData{
			"page_count":    convResult.PageCount,
			"pages_written": convResult.PageCount,
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

	w.log.Info("file ingested", "job_id", j.ID, "file", f.Filename, "pages", convResult.PageCount, "ms", duration)
	return nil
}

// processFolderJob ingests all files in a folder as one multi-document job.
func (w *Worker) processFolderJob(ctx context.Context, f DetectedFile) error {
	// ── Step 0: License check ────────────────────────────────────────────────
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

	// ── Step 1b: Apply meta from _READY.json content ─────────────────────────
	if f.MetaContent != "" {
		payload, err := parseMeta([]byte(f.MetaContent))
		if err == nil && payload != nil {
			state := job.StateData{}
			if payload.JobAlias != nil {
				state["job_alias"] = *payload.JobAlias
			}
			if payload.Priority != nil {
				state["priority"] = *payload.Priority
			}
			if payload.UserData != nil {
				state["user_data"] = *payload.UserData
			}
			if len(payload.CustomFields) > 0 {
				state["meta_fields"] = payload.CustomFields
			}
			if len(state) > 0 {
				w.svc.MergeJobState(ctx, j.ID, state)
			}
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

	// ── Step 3: Temp working directory ──────────────────────────────────────
	workDir, err := os.MkdirTemp("", fmt.Sprintf("fs-ingest-%s-*", j.ID[:8]))
	if err != nil {
		return w.failJob(ctx, j.ID, "create temp dir", err)
	}
	defer os.RemoveAll(workDir)

	// ── Step 4: Transition to CONVERTING ────────────────────────────────────
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

	// ── Step 5: Process each file ────────────────────────────────────────────
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
			JobID:    j.ID,
			Artifact: job.Artifact{Key: origKey, Type: "original_pdf", PageNum: 0, SizeBytes: origSize, CreatedAt: time.Now().UTC()},
		})

		outputDir := filepath.Join(workDir, fmt.Sprintf("pages%02d", docIdx))
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return w.failJob(ctx, j.ID, fmt.Sprintf("create output dir doc %d", docIdx), err)
		}

		convResult, err := ConvertToTIF(ctx, srcPath, outputDir, w.log)
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

		for i, pagePath := range convResult.PagePaths {
			globalPageNum++
			tifKey, tifSize, err := w.storage.UploadTIF(ctx, j.ID, f.TenantID, f.SystemID, globalPageNum, pagePath)
			if err != nil {
				return w.failJob(ctx, j.ID, fmt.Sprintf("upload page %d", globalPageNum), err)
			}
			w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
				JobID:    j.ID,
				Artifact: job.Artifact{Key: tifKey, Type: "original_tif", PageNum: globalPageNum, SizeBytes: tifSize, CreatedAt: time.Now().UTC()},
			})
			docID := doc.ID
			state := "ingested"
			srcPageNum := i + 1
			page := &job.Page{
				JobID:             j.ID,
				DocumentID:        &docID,
				OriginalPageIndex: i,
				SourcePageNumber:  &srcPageNum,
				OrderKey:          float64(globalPageNum),
				State:             &state,
			}
			if err := w.svc.CreatePage(ctx, page); err != nil {
				w.log.Warn("failed to create page record", "job_id", j.ID, "page", globalPageNum, "error", err)
			}
		}
		totalPages += convResult.PageCount
	}

	// ── Step 6: Finalize ─────────────────────────────────────────────────────
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

// failJob transitions a job to FAILED and wraps the error.
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

// applyMeta checks for a .meta sidecar in MinIO and merges its contents into job_state.
// Silent no-op if the object doesn't exist — that's the normal case.
func (w *Worker) applyMeta(ctx context.Context, jobID, bucket, metaKey string) {
	if _, err := w.storage.StatObject(ctx, bucket, metaKey); err != nil {
		return // object doesn't exist — normal case
	}
	rc, err := w.storage.ReadObject(ctx, bucket, metaKey)
	if err != nil {
		w.log.Warn("could not read meta sidecar", "job_id", jobID, "key", metaKey, "error", err)
		return
	}
	defer rc.Close()
	data, _ := io.ReadAll(io.LimitReader(rc, 1<<16)) // 64 KB cap

	w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
		JobID: jobID,
		Artifact: job.Artifact{
			Key:       metaKey,
			Type:      "meta_json",
			SizeBytes: int64(len(data)),
			CreatedAt: time.Now().UTC(),
		},
	})

	payload, err := parseMeta(data)
	if err != nil || payload == nil {
		return
	}

	state := job.StateData{}
	if payload.JobAlias != nil {
		state["job_alias"] = *payload.JobAlias
	}
	if payload.Priority != nil {
		state["priority"] = *payload.Priority
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
