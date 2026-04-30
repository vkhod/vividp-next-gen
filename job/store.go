package job

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the only writer to the jobs, job_documents, job_pages,
// and job_fields tables. No other service accesses these directly.
type Store struct {
	db *pgxpool.Pool
}

func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// ── Job operations ────────────────────────────────────────────────────────────

// CreateJob inserts a new job at status=DETECTED.
// Called the moment a file is detected — before any conversion or processing.
func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	classJSON, err := json.Marshal(j.Classifications)
	if err != nil {
		return fmt.Errorf("marshal classifications: %w", err)
	}
	stateJSON, err := json.Marshal(j.JobState)
	if err != nil {
		return fmt.Errorf("marshal job_state: %w", err)
	}
	artsJSON, err := json.Marshal(j.Artifacts)
	if err != nil {
		return fmt.Errorf("marshal artifacts: %w", err)
	}
	timingsJSON, err := json.Marshal(j.PipelineTimings)
	if err != nil {
		return fmt.Errorf("marshal pipeline_timings: %w", err)
	}

	return s.db.QueryRow(ctx, `
		INSERT INTO jobs (
			tenant_id, system_id,
			job_name, job_alias,
			status, stage,
			source_filename, source_bucket, source_key,
			size_bytes, original_file_path,
			nfiles, priority, on_hold, on_error,
			metadata_override,
			classifications, job_state, artifacts, pipeline_timings
		) VALUES (
			$1,  $2,
			$3,  $4,
			$5,  $6,
			$7,  $8,  $9,
			$10, $11,
			$12, $13, $14, $15,
			$16,
			$17, $18, $19, $20
		)
		RETURNING id, created_at, updated_at`,
		j.TenantID, j.SystemID,
		j.JobName, j.JobAlias,
		string(j.Status), j.Stage,
		j.SourceFilename, j.SourceBucket, j.SourceKey,
		j.SizeBytes, j.OriginalFilePath,
		j.NFiles, j.Priority, j.OnHold, j.OnError,
		j.MetadataOverride,
		classJSON, stateJSON, artsJSON, timingsJSON,
	).Scan(&j.ID, &j.CreatedAt, &j.UpdatedAt)
}

// TransitionStatus advances a job to a new status.
// Merges newState into job_state JSONB (PostgreSQL || operator — never replaces).
// Records the transition in job_transitions audit table.
// Returns error if the transition is not legal.
func (s *Store) TransitionStatus(ctx context.Context, req TransitionRequest) error {
	// Fetch current status first
	var cur Status
	err := s.db.QueryRow(ctx,
		`SELECT status FROM jobs WHERE id = $1 FOR UPDATE`, req.JobID,
	).Scan(&cur)
	if err != nil {
		return fmt.Errorf("fetch job %s: %w", req.JobID, err)
	}

	if !IsLegalTransition(cur, req.ToStatus) {
		return fmt.Errorf("illegal transition %s → %s for job %s", cur, req.ToStatus, req.JobID)
	}

	// Build new state JSON
	stateJSON, err := json.Marshal(req.NewState)
	if err != nil {
		return fmt.Errorf("marshal new state: %w", err)
	}

	// Build timing update (if station timing provided)
	var timingUpdate string
	var timingArgs []any
	if req.StationName != "" && req.DurationMS > 0 {
		// Merge a single key into pipeline_timings JSONB
		timingJSON, _ := json.Marshal(map[string]int64{req.StationName: req.DurationMS})
		timingUpdate = ", pipeline_timings = pipeline_timings || $4::jsonb"
		timingArgs = append(timingArgs, timingJSON)
	}

	completedAt := (*time.Time)(nil)
	if req.ToStatus == StatusCompleted {
		now := time.Now().UTC()
		completedAt = &now
	}

	// Two operations in one transaction: update job + insert audit row
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Update job state — merge JSONB, never replace
	// Args order: $1=status, $2=new_state, $3=completed_at, [$4=timing], $N=job_id (always last)
	args := []any{string(req.ToStatus), stateJSON, completedAt}
	args = append(args, timingArgs...)
	args = append(args, req.JobID)

	_, err = tx.Exec(ctx, fmt.Sprintf(`
		UPDATE jobs SET
			status       = $1,
			job_state    = job_state || $2::jsonb,
			completed_at = COALESCE($3, completed_at)
			%s
		WHERE id = $%d`,
		timingUpdate, 4+len(timingArgs),
	), args...)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}

	// Release worker claim after successful transition
	_, err = tx.Exec(ctx,
		`UPDATE jobs SET claimed_by = NULL, claimed_at = NULL WHERE id = $1`,
		req.JobID,
	)
	if err != nil {
		return fmt.Errorf("release claim: %w", err)
	}

	// Insert immutable audit row
	_, err = tx.Exec(ctx, `
		INSERT INTO job_transitions (job_id, from_status, to_status, worker_id, note)
		VALUES ($1, $2, $3, $4, $5)`,
		req.JobID, string(cur), string(req.ToStatus), req.WorkerID, req.Note,
	)
	if err != nil {
		return fmt.Errorf("insert transition: %w", err)
	}

	return tx.Commit(ctx)
}

// ClaimJob atomically claims the next available job in a given status.
// SELECT FOR UPDATE SKIP LOCKED ensures multiple concurrent workers
// never pick up the same job.
func (s *Store) ClaimJob(ctx context.Context, status Status, workerID string) (*Job, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, system_id, status, stage,
		       source_filename, source_bucket, source_key, size_bytes,
		       page_count, nfiles, priority,
		       job_state, artifacts, pipeline_timings
		FROM jobs
		WHERE  status    = $1
		  AND  on_hold   = FALSE
		  AND  claimed_by IS NULL
		ORDER BY priority DESC, created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`,
		string(status),
	)

	j, err := scanJobRow(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // nothing available — not an error
		}
		return nil, fmt.Errorf("claim query: %w", err)
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx,
		`UPDATE jobs SET claimed_by = $1, claimed_at = $2 WHERE id = $3`,
		workerID, now, j.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("mark claimed: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}

	j.ClaimedBy = &workerID
	j.ClaimedAt = &now
	return j, nil
}

// GetJob fetches a job by ID.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, tenant_id, system_id, status, stage,
		       source_filename, source_bucket, source_key, size_bytes,
		       page_count, nfiles, priority,
		       job_state, artifacts, pipeline_timings
		FROM jobs WHERE id = $1`, id)

	j, err := scanJobRow(row)
	if err != nil {
		return nil, fmt.Errorf("get job %s: %w", id, err)
	}
	return j, nil
}

// AppendArtifact adds one artifact to the job's artifact manifest JSONB array.
// Uses PostgreSQL jsonb_insert to append without loading the full array.
func (s *Store) AppendArtifact(ctx context.Context, jobID string, a Artifact) error {
	artJSON, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		UPDATE jobs
		SET artifacts = artifacts || $1::jsonb
		WHERE id = $2`,
		fmt.Sprintf("[%s]", artJSON), jobID,
	)
	return err
}

// SetPageCount updates the job page_count once conversion is complete.
func (s *Store) SetPageCount(ctx context.Context, jobID string, count int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE jobs SET page_count = $1 WHERE id = $2`,
		count, jobID,
	)
	return err
}

// ── Document operations ───────────────────────────────────────────────────────

// CreateDocument inserts a new logical document grouping within a job.
func (s *Store) CreateDocument(ctx context.Context, d *Document) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO job_documents (
			job_id, document_index, original_document_index,
			form_name, template_name, bundle_name
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`,
		d.JobID, d.DocumentIndex, d.OriginalDocumentIndex,
		d.FormName, d.TemplateName, d.BundleName,
	).Scan(&d.ID, &d.CreatedAt)
}

// ── Page operations ───────────────────────────────────────────────────────────

// CreatePage inserts a new page record.
// orderKey uses fractional indexing (1.0, 2.0, 3.0...) — one row updated per reorder.
func (s *Store) CreatePage(ctx context.Context, p *Page) error {
	matchJSON, _ := json.Marshal(p.MatchCandidates)
	prepJSON, _ := json.Marshal(p.Preprocessing)

	if matchJSON == nil {
		matchJSON = []byte("[]")
	}
	if prepJSON == nil {
		prepJSON = []byte("{}")
	}

	return s.db.QueryRow(ctx, `
		INSERT INTO job_pages (
			job_id, document_id,
			original_page_index, source_page_number, order_key,
			state, is_deleted,
			image_height_px, image_width_px,
			original_file_path,
			operator_rotation,
			match_candidates, preprocessing
		) VALUES (
			$1,  $2,
			$3,  $4,  $5,
			$6,  $7,
			$8,  $9,
			$10,
			$11,
			$12, $13
		)
		RETURNING id, created_at`,
		p.JobID, p.DocumentID,
		p.OriginalPageIndex, p.SourcePageNumber, p.OrderKey,
		p.State, p.IsDeleted,
		p.ImageHeightPx, p.ImageWidthPx,
		p.OriginalFilePath,
		p.OperatorRotation,
		matchJSON, prepJSON,
	).Scan(&p.ID, &p.CreatedAt)
}

// ── Field operations ──────────────────────────────────────────────────────────

// CreateField inserts a single field record.
// Called by the Recognition Hub after each field is processed.
func (s *Store) CreateField(ctx context.Context, f *Field) error {
	recJSON := f.Recognition
	if recJSON == nil {
		recJSON = json.RawMessage("{}")
	}
	geoJSON := f.Geometry
	if geoJSON == nil {
		geoJSON = json.RawMessage("null")
	}
	setupJSON := f.SetupInfo
	if setupJSON == nil {
		setupJSON = json.RawMessage("{}")
	}

	return s.db.QueryRow(ctx, `
		INSERT INTO job_fields (
			job_id, page_id,
			field_name, array_index, setup_table,
			is_job_level,
			final_value, field_state, value_source, confidence,
			field_order, is_visible, is_readonly,
			recognition, geometry, setup_info
		) VALUES (
			$1,  $2,
			$3,  $4,  $5,
			$6,
			$7,  $8,  $9,  $10,
			$11, $12, $13,
			$14, $15, $16
		)
		RETURNING id, created_at, updated_at`,
		f.JobID, f.PageID,
		f.FieldName, f.ArrayIndex, f.SetupTable,
		f.IsJobLevel,
		f.FinalValue, f.FieldState, f.ValueSource, f.Confidence,
		f.FieldOrder, f.IsVisible, f.IsReadonly,
		recJSON, geoJSON, setupJSON,
	).Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt)
}

// UpdateFieldValue updates the final value and state after operator correction.
// Called by the Verification Workstation backend.
func (s *Store) UpdateFieldValue(ctx context.Context, fieldID int64, value, state, source string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE job_fields
		SET final_value  = $1,
		    field_state  = $2,
		    value_source = $3
		WHERE id = $4`,
		value, state, source, fieldID,
	)
	return err
}

// GetFieldsForPage fetches all visible fields for a page in display order.
// This is the primary Verification Workstation query.
func (s *Store) GetFieldsForPage(ctx context.Context, pageID int64) ([]Field, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, job_id, page_id,
		       field_name, array_index, setup_table, is_job_level,
		       final_value, field_state, value_source, confidence,
		       field_order, is_visible, is_readonly,
		       recognition, geometry, setup_info,
		       created_at, updated_at
		FROM job_fields
		WHERE page_id   = $1
		  AND is_visible = TRUE
		ORDER BY field_order, array_index NULLS FIRST`,
		pageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFields(rows)
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

// scanJobRow reads a lightweight job row (not all 50 columns — only what
// workers need for claiming and processing decisions).
func scanJobRow(row pgx.Row) (*Job, error) {
	j := &Job{
		Classifications: []Classification{},
		Artifacts:       []Artifact{},
		PipelineTimings: map[string]int64{},
		JobState:        StateData{},
	}

	var (
		stateJSON, artsJSON, timingsJSON []byte
	)

	err := row.Scan(
		&j.ID, &j.TenantID, &j.SystemID,
		&j.Status, &j.Stage,
		&j.SourceFilename, &j.SourceBucket, &j.SourceKey, &j.SizeBytes,
		&j.PageCount, &j.NFiles, &j.Priority,
		&stateJSON, &artsJSON, &timingsJSON,
	)
	if err != nil {
		return nil, err
	}

	json.Unmarshal(stateJSON, &j.JobState)
	json.Unmarshal(artsJSON, &j.Artifacts)
	json.Unmarshal(timingsJSON, &j.PipelineTimings)
	return j, nil
}

func scanFields(rows pgx.Rows) ([]Field, error) {
	var fields []Field
	for rows.Next() {
		var f Field
		err := rows.Scan(
			&f.ID, &f.JobID, &f.PageID,
			&f.FieldName, &f.ArrayIndex, &f.SetupTable, &f.IsJobLevel,
			&f.FinalValue, &f.FieldState, &f.ValueSource, &f.Confidence,
			&f.FieldOrder, &f.IsVisible, &f.IsReadonly,
			&f.Recognition, &f.Geometry, &f.SetupInfo,
			&f.CreatedAt, &f.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	return fields, rows.Err()
}

// MergeJobState patches job_state JSONB without changing status or writing an audit row.
// Use this for metadata that enriches the job but is not a state transition.
func (s *Store) MergeJobState(ctx context.Context, jobID string, data StateData) error {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE jobs SET job_state = job_state || $1::jsonb WHERE id = $2`,
		jsonBytes, jobID,
	)
	return err
}

func (s *Store) KeysWithJobs(ctx context.Context, bucket string, keys []string) (map[string]bool, error) {
	//TODO: later, consider check the size of keys parameter. I comes from a bucket scan, so it can be huge
	rows, err := s.db.Query(ctx,
		`SELECT source_key FROM jobs WHERE source_bucket = $1 AND source_key = ANY($2::text[])`,
		bucket, keys,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool, len(keys))
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		result[k] = true
	}
	return result, rows.Err()
}
