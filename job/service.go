// ═══════════════════════════════════════════════════════════════════════════════
// job/service.go
// The single authority for job state. All pipeline stations go through here.
// No station writes to the jobs table directly.
// ═══════════════════════════════════════════════════════════════════════════════
package job

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Service orchestrates Store + Publisher.
// Rule: write to DB first, publish to NATS after.
// If publish fails after a DB write, Temporal detects the stuck job and retries.
type Service struct {
	store     *Store
	publisher *Publisher
	log       *slog.Logger
}

func NewService(store *Store, publisher *Publisher, log *slog.Logger) *Service {
	return &Service{store: store, publisher: publisher, log: log.With("module", "job-service")}
}

// CreateJob is called the moment a file is detected.
// Creates the job record at status=DETECTED before any file processing begins.
// This means a job can always be found and recovered even if the system crashes
// during ingestion.
func (s *Service) CreateJob(ctx context.Context, req CreateJobRequest) (*Job, error) {
	if req.SystemID == "" {
		req.SystemID = "default"
	}
	if req.NFiles == 0 {
		req.NFiles = 1
	}

	j := &Job{
		TenantID:         req.TenantID,
		SystemID:         req.SystemID,
		Status:           StatusDetected,
		Stage:            "INGESTION",
		SourceFilename:   req.Filename,
		SourceBucket:     req.Bucket,
		SourceKey:        req.Key,
		SizeBytes:        req.SizeBytes,
		OriginalFilePath: req.OriginalFilePath,
		NFiles:           req.NFiles,
		JobState: StateData{
			"detected_at": time.Now().UTC().Format(time.RFC3339),
			"source":      req.Filename,
		},
		Classifications: []Classification{},
		Artifacts:       []Artifact{},
		PipelineTimings: map[string]int64{},
	}

	// 1. Write to DB — always first
	if err := s.store.CreateJob(ctx, j); err != nil {
		return nil, fmt.Errorf("store.CreateJob: %w", err)
	}

	// 2. Publish event — after successful DB write
	if err := s.publisher.PublishTransition(ctx, j); err != nil {
		// Log but don't fail — Temporal will detect the missing event
		s.log.Warn("publish failed after job creation", "job_id", j.ID, "error", err)
	}

	return j, nil
}

// Transition advances a job to a new status.
// This is the ONLY way any station may change a job's status.
// Validates the state transition before writing to DB.
func (s *Service) Transition(ctx context.Context, req TransitionRequest) (*Job, error) {
	// 1. Write to DB (validates legal transition inside)
	if err := s.store.TransitionStatus(ctx, req); err != nil {
		return nil, fmt.Errorf("transition %s→%s: %w", req.JobID, req.ToStatus, err)
	}

	// 2. Fetch the updated job (needed for publish payload)
	j, err := s.store.GetJob(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("fetch after transition: %w", err)
	}

	// 3. Publish broadcast event + work message
	if err := s.publisher.PublishTransition(ctx, j); err != nil {
		s.log.Warn("publish failed after transition", "job_id", j.ID, "status", string(j.Status), "error", err)
	}

	return j, nil
}

// GetJob fetches a job by ID.
func (s *Service) GetJob(ctx context.Context, id string) (*Job, error) {
	return s.store.GetJob(ctx, id)
}

// ClaimJob atomically claims the next available job for a worker.
// Returns nil, nil if no job is available — not an error.
func (s *Service) ClaimJob(ctx context.Context, status Status, workerID string) (*Job, error) {
	return s.store.ClaimJob(ctx, status, workerID)
}

// ClaimJobByID claims a specific job by ID if it is in the expected status.
// Returns nil, nil if the job is not available (wrong status, on hold, already claimed).
func (s *Service) ClaimJobByID(ctx context.Context, jobID string, status Status, workerID string) (*Job, error) {
	return s.store.ClaimJobByID(ctx, jobID, status, workerID)
}

// RecordArtifact appends a file artifact to the job manifest.
// Called by the Ingestion Service as each TIF page is written to MinIO.
func (s *Service) RecordArtifact(ctx context.Context, req AddArtifactRequest) error {
	return s.store.AppendArtifact(ctx, req.JobID, req.Artifact)
}

// SetPageCount updates the job's page count once conversion is complete.
func (s *Service) SetPageCount(ctx context.Context, jobID string, count int) error {
	return s.store.SetPageCount(ctx, jobID, count)
}

// CreateDocument creates a logical document grouping within a batch job.
// Returns the created document with its assigned ID.
func (s *Service) CreateDocument(ctx context.Context, d *Document) error {
	return s.store.CreateDocument(ctx, d)
}

// CreatePage creates a page record within a document.
// orderKey uses fractional indexing starting at 1.0, 2.0, 3.0...
// To insert between page 1 and 2: set orderKey = 1.5
func (s *Service) CreatePage(ctx context.Context, p *Page) error {
	return s.store.CreatePage(ctx, p)
}

// CreateField inserts a recognized field value.
// Called by the Recognition Hub after processing each field zone.
func (s *Service) CreateField(ctx context.Context, f *Field) error {
	return s.store.CreateField(ctx, f)
}

// MergeJobState merges metadata into job_state without a status transition.
func (s *Service) MergeJobState(ctx context.Context, jobID string, data StateData) error {
	return s.store.MergeJobState(ctx, jobID, data)
}

// HoldJob sets on_hold=TRUE. Admin-only action.
func (s *Service) HoldJob(ctx context.Context, jobID, adminID string) error {
	return s.store.SetOnHold(ctx, jobID, true, adminID)
}

// ReleaseJob sets on_hold=FALSE. Admin-only action.
func (s *Service) ReleaseJob(ctx context.Context, jobID, adminID string) error {
	return s.store.SetOnHold(ctx, jobID, false, adminID)
}

// DeleteJob hard-deletes a job and all its related rows. Admin-only action.
func (s *Service) DeleteJob(ctx context.Context, jobID string) error {
	return s.store.DeleteJob(ctx, jobID)
}

// ListFields returns all fields for a job, ordered by field_order.
func (s *Service) ListFields(ctx context.Context, jobID string) ([]Field, error) {
	return s.store.GetFieldsForJob(ctx, jobID)
}

// KeysWithJobs returns the subset of keys that already have a job row.
func (s *Service) KeysWithJobs(ctx context.Context, bucket string, keys []string) (map[string]bool, error) {
	return s.store.KeysWithJobs(ctx, bucket, keys)
}
