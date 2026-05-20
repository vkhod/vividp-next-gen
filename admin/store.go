package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides read-only admin queries against the jobs schema.
// Writes go through job.Service to preserve the single-writer rule.
type Store struct {
	db *pgxpool.Pool
}

func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// ── Response types (JSON tags match TypeScript frontend exactly) ──────────────

type TopClassification struct {
	Name       string `json:"name"`
	Confidence int    `json:"confidence"`
}

// AdminJob is the list-view row — no JSONB blobs, just display columns.
type AdminJob struct {
	ID               string             `json:"id"`
	TenantID         string             `json:"tenant_id"`
	TenantName       string             `json:"tenant_name"`
	SystemID         string             `json:"system_id"`
	SystemName       string             `json:"system_name"`
	JobName          *string            `json:"job_name"`
	JobAlias         *string            `json:"job_alias"`
	Status           string             `json:"status"`
	Stage            string             `json:"stage"`
	Priority         int                `json:"priority"`
	OnHold           bool               `json:"on_hold"`
	OnError          int                `json:"on_error"`
	RetryCount       int                `json:"retry_count"`
	NFiles           int                `json:"nfiles"`
	PageCount        *int               `json:"page_count"`
	ScannedPages     *int               `json:"scanned_pages"`
	SourceFilename   string             `json:"source_filename"`
	SourceBucket     string             `json:"source_bucket"`
	SourceKey        string             `json:"source_key"`
	SizeBytes        int64              `json:"size_bytes"`
	UserData         *string            `json:"user_data"`
	ClaimedBy        *string            `json:"claimed_by"`
	ClaimedAt        *time.Time         `json:"claimed_at"`
	LastError        *string            `json:"last_error"`
	ErrorComment     *string            `json:"error_comment"`
	IsDuplicate      bool               `json:"is_duplicate"`
	IsArchived       bool               `json:"is_archived"`
	SkippedVerify    bool               `json:"skipped_verify"`
	SkippedTruTypist bool               `json:"skipped_trutypist"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	CompletedAt      *time.Time         `json:"completed_at"`
	TopClass         *TopClassification `json:"top_classification"`
}

// AdminJobDetail extends AdminJob with all JSONB columns and phase timestamps.
type AdminJobDetail struct {
	AdminJob
	// Phase timestamps
	CaptureBeganAt      *time.Time      `json:"capture_began_at"`
	CaptureEndedAt      *time.Time      `json:"capture_ended_at"`
	OCRBeganAt          *time.Time      `json:"ocr_began_at"`
	OCREndedAt          *time.Time      `json:"ocr_ended_at"`
	VerificationBeganAt *time.Time      `json:"verification_began_at"`
	VerificationEndedAt *time.Time      `json:"verification_ended_at"`
	// Operator metrics
	VerifierName        *string         `json:"verifier_name"`
	VerificationSeconds *int            `json:"verification_seconds"`
	KeystrokesCount     *int            `json:"keystrokes_count"`
	// Additional scalars
	CaptureType          *int            `json:"capture_type"`
	MetadataOverride     bool            `json:"metadata_override"`
	SuspectedFieldsCount *int            `json:"suspected_fields_count"`
	ContentHash          *string         `json:"content_hash"`
	HashComputedAt       *time.Time      `json:"hash_computed_at"`
	TifFileKey           *string         `json:"tif_file_key"`
	TifRegFileKey        *string         `json:"tif_reg_file_key"`
	Filter               *string         `json:"filter"`
	// JSONB columns — passed through as raw JSON to avoid deserialization loss
	PipelineTimings json.RawMessage `json:"pipeline_timings"`
	Classifications json.RawMessage `json:"classifications"`
	Artifacts       json.RawMessage `json:"artifacts"`
	JobState        json.RawMessage `json:"job_state"`
}

// JobTransition is one row from the job_transitions audit table.
type JobTransition struct {
	ID         int64      `json:"id"`
	JobID      string     `json:"job_id"`
	FromStatus *string    `json:"from_status"`
	ToStatus   string     `json:"to_status"`
	WorkerID   *string    `json:"worker_id"`
	Note       *string    `json:"note"`
	OccurredAt time.Time  `json:"occurred_at"`
}

// FieldsSummary aggregates job_fields counts for a single job.
type FieldsSummary struct {
	Total             int            `json:"total"`
	Recognized        int            `json:"recognized"`
	Validated         int            `json:"validated"`
	OperatorCorrected int            `json:"operator_corrected"`
	AvgConfidence     *float64       `json:"avg_confidence"`
	ByState           map[string]int `json:"by_state"`
	BySource          map[string]int `json:"by_source"`
}

// ArtifactResponse is an artifact entry enriched with a presigned download URL.
type ArtifactResponse struct {
	Key          string     `json:"key"`
	Type         string     `json:"type"`
	SizeBytes    int64      `json:"size_bytes"`
	CreatedAt    time.Time  `json:"created_at"`
	PresignedURL *string    `json:"presigned_url"`
}

// ── List query ────────────────────────────────────────────────────────────────

// ListJobsQuery holds filter and sort parameters for ListJobs.
type ListJobsQuery struct {
	TenantID string
	SystemID string
	Statuses []string
	Search   string
	DateFrom string // YYYY-MM-DD
	DateTo   string // YYYY-MM-DD
	Sort     string // created_at | updated_at | priority
	Dir      string // asc | desc
}

const listJobsSelect = `
	SELECT
		j.id, j.tenant_id, COALESCE(t.name, '') AS tenant_name,
		j.system_id, COALESCE(s.name, '') AS system_name,
		j.job_name, j.job_alias, j.status, j.stage,
		j.priority, j.on_hold, j.on_error, j.retry_count,
		j.nfiles, j.page_count, j.scanned_pages,
		j.source_filename, j.source_bucket, j.source_key, j.size_bytes,
		j.user_data, j.claimed_by, j.claimed_at,
		LEFT(j.last_error, 200) AS last_error, j.error_comment,
		j.is_duplicate, j.is_archived, j.skipped_verify, j.skipped_trutypist,
		j.created_at, j.updated_at, j.completed_at,
		j.classifications->0->>'name'           AS top_class_name,
		(j.classifications->0->>'confidence')::int AS top_class_confidence
	FROM jobs j
	LEFT JOIN tenants t ON t.id = j.tenant_id
	LEFT JOIN systems  s ON s.id = j.system_id`

// ListJobs returns up to 250 jobs matching the given filters.
func (s *Store) ListJobs(ctx context.Context, q ListJobsQuery) ([]AdminJob, error) {
	var conds []string
	var args []any
	n := 1

	if q.TenantID != "" {
		conds = append(conds, fmt.Sprintf("j.tenant_id = $%d", n))
		args = append(args, q.TenantID)
		n++
	}
	if q.SystemID != "" {
		conds = append(conds, fmt.Sprintf("j.system_id = $%d", n))
		args = append(args, q.SystemID)
		n++
	}
	if len(q.Statuses) > 0 {
		conds = append(conds, fmt.Sprintf("j.status = ANY($%d)", n))
		args = append(args, q.Statuses)
		n++
	}
	if q.DateFrom != "" {
		conds = append(conds, fmt.Sprintf("j.created_at >= $%d", n))
		args = append(args, q.DateFrom)
		n++
	}
	if q.DateTo != "" {
		conds = append(conds, fmt.Sprintf("j.created_at < $%d::date + interval '1 day'", n))
		args = append(args, q.DateTo)
		n++
	}
	if q.Search != "" {
		// Same $n can appear multiple times in a PostgreSQL query
		conds = append(conds, fmt.Sprintf(
			"(j.job_alias ILIKE $%d OR j.source_filename ILIKE $%d OR j.id::text ILIKE $%d)",
			n, n, n,
		))
		args = append(args, "%"+q.Search+"%")
		n++
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	sortCol := "j.created_at"
	switch q.Sort {
	case "priority":
		sortCol = "j.priority"
	case "updated_at":
		sortCol = "j.updated_at"
	}
	dir := "DESC"
	if strings.ToLower(q.Dir) == "asc" {
		dir = "ASC"
	}

	sql := fmt.Sprintf("%s %s ORDER BY %s %s LIMIT 250", listJobsSelect, where, sortCol, dir)

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []AdminJob
	for rows.Next() {
		var j AdminJob
		var topName *string
		var topConf *int
		if err := rows.Scan(
			&j.ID, &j.TenantID, &j.TenantName,
			&j.SystemID, &j.SystemName,
			&j.JobName, &j.JobAlias, &j.Status, &j.Stage,
			&j.Priority, &j.OnHold, &j.OnError, &j.RetryCount,
			&j.NFiles, &j.PageCount, &j.ScannedPages,
			&j.SourceFilename, &j.SourceBucket, &j.SourceKey, &j.SizeBytes,
			&j.UserData, &j.ClaimedBy, &j.ClaimedAt,
			&j.LastError, &j.ErrorComment,
			&j.IsDuplicate, &j.IsArchived, &j.SkippedVerify, &j.SkippedTruTypist,
			&j.CreatedAt, &j.UpdatedAt, &j.CompletedAt,
			&topName, &topConf,
		); err != nil {
			return nil, fmt.Errorf("scan job row: %w", err)
		}
		if topName != nil && topConf != nil {
			j.TopClass = &TopClassification{Name: *topName, Confidence: *topConf}
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// ── Detail query ──────────────────────────────────────────────────────────────

// GetJobDetail fetches a full job record including all JSONB columns.
func (s *Store) GetJobDetail(ctx context.Context, id string) (*AdminJobDetail, error) {
	row := s.db.QueryRow(ctx, `
		SELECT
			j.id, j.tenant_id, COALESCE(t.name, '') AS tenant_name,
			j.system_id, COALESCE(s.name, '') AS system_name,
			j.job_name, j.job_alias, j.status, j.stage,
			j.priority, j.on_hold, j.on_error, j.retry_count,
			j.nfiles, j.page_count, j.scanned_pages,
			j.source_filename, j.source_bucket, j.source_key, j.size_bytes,
			j.user_data, j.claimed_by, j.claimed_at,
			j.last_error, j.error_comment,
			j.is_duplicate, j.is_archived, j.skipped_verify, j.skipped_trutypist,
			j.created_at, j.updated_at, j.completed_at,
			j.classifications->0->>'name'           AS top_class_name,
			(j.classifications->0->>'confidence')::int AS top_class_confidence,
			j.capture_began_at, j.capture_ended_at,
			j.ocr_began_at, j.ocr_ended_at,
			j.verification_began_at, j.verification_ended_at,
			j.verifier_name, j.verification_seconds, j.keystrokes_count,
			j.capture_type, j.metadata_override, j.suspected_fields_count,
			j.content_hash, j.hash_computed_at, j.tif_file_key, j.tif_reg_file_key, j.filter,
			j.pipeline_timings, j.classifications, j.artifacts, j.job_state
		FROM jobs j
		LEFT JOIN tenants t ON t.id = j.tenant_id
		LEFT JOIN systems  s ON s.id = j.system_id
		WHERE j.id = $1`, id)

	var d AdminJobDetail
	var topName *string
	var topConf *int
	var (
		timingsJSON, classJSON, artsJSON, stateJSON []byte
	)

	if err := row.Scan(
		&d.ID, &d.TenantID, &d.TenantName,
		&d.SystemID, &d.SystemName,
		&d.JobName, &d.JobAlias, &d.Status, &d.Stage,
		&d.Priority, &d.OnHold, &d.OnError, &d.RetryCount,
		&d.NFiles, &d.PageCount, &d.ScannedPages,
		&d.SourceFilename, &d.SourceBucket, &d.SourceKey, &d.SizeBytes,
		&d.UserData, &d.ClaimedBy, &d.ClaimedAt,
		&d.LastError, &d.ErrorComment,
		&d.IsDuplicate, &d.IsArchived, &d.SkippedVerify, &d.SkippedTruTypist,
		&d.CreatedAt, &d.UpdatedAt, &d.CompletedAt,
		&topName, &topConf,
		&d.CaptureBeganAt, &d.CaptureEndedAt,
		&d.OCRBeganAt, &d.OCREndedAt,
		&d.VerificationBeganAt, &d.VerificationEndedAt,
		&d.VerifierName, &d.VerificationSeconds, &d.KeystrokesCount,
		&d.CaptureType, &d.MetadataOverride, &d.SuspectedFieldsCount,
		&d.ContentHash, &d.HashComputedAt, &d.TifFileKey, &d.TifRegFileKey, &d.Filter,
		&timingsJSON, &classJSON, &artsJSON, &stateJSON,
	); err != nil {
		return nil, fmt.Errorf("get job detail %s: %w", id, err)
	}

	if topName != nil && topConf != nil {
		d.TopClass = &TopClassification{Name: *topName, Confidence: *topConf}
	}
	d.PipelineTimings = json.RawMessage(timingsJSON)
	d.Classifications = json.RawMessage(classJSON)
	d.Artifacts = json.RawMessage(artsJSON)
	d.JobState = json.RawMessage(stateJSON)

	return &d, nil
}

// GetArtifactsJSON returns the raw artifacts JSONB bytes for a job.
func (s *Store) GetArtifactsJSON(ctx context.Context, id string) ([]byte, error) {
	var raw []byte
	err := s.db.QueryRow(ctx,
		`SELECT artifacts FROM jobs WHERE id = $1`, id,
	).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("get artifacts %s: %w", id, err)
	}
	return raw, nil
}

// ── Transitions ───────────────────────────────────────────────────────────────

// GetTransitions returns all job_transitions rows for a job, oldest first.
func (s *Store) GetTransitions(ctx context.Context, jobID string) ([]JobTransition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, job_id, from_status, to_status, worker_id, note, occurred_at
		FROM job_transitions
		WHERE job_id = $1
		ORDER BY occurred_at ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("get transitions: %w", err)
	}
	defer rows.Close()

	var out []JobTransition
	for rows.Next() {
		var t JobTransition
		if err := rows.Scan(
			&t.ID, &t.JobID, &t.FromStatus, &t.ToStatus,
			&t.WorkerID, &t.Note, &t.OccurredAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ── Fields summary ────────────────────────────────────────────────────────────

// GetFieldsSummary aggregates job_fields counts for a single job.
func (s *Store) GetFieldsSummary(ctx context.Context, jobID string) (*FieldsSummary, error) {
	// Scalar aggregates
	var total, recognized, validated, opCorrected int
	var avgConf *float64

	err := s.db.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(CASE WHEN field_state IN ('recognized','validated') THEN 1 END),
			COUNT(CASE WHEN field_state = 'validated' THEN 1 END),
			COUNT(CASE WHEN value_source = 'operator' THEN 1 END),
			AVG(confidence::float)
		FROM job_fields
		WHERE job_id = $1`, jobID,
	).Scan(&total, &recognized, &validated, &opCorrected, &avgConf)
	if err != nil {
		return nil, fmt.Errorf("fields summary: %w", err)
	}

	// By-state breakdown
	byState, err := s.groupCount(ctx,
		`SELECT field_state, COUNT(*) FROM job_fields WHERE job_id = $1 AND field_state IS NOT NULL GROUP BY field_state`,
		jobID)
	if err != nil {
		return nil, err
	}

	// By-source breakdown
	bySource, err := s.groupCount(ctx,
		`SELECT value_source, COUNT(*) FROM job_fields WHERE job_id = $1 AND value_source IS NOT NULL GROUP BY value_source`,
		jobID)
	if err != nil {
		return nil, err
	}

	return &FieldsSummary{
		Total:             total,
		Recognized:        recognized,
		Validated:         validated,
		OperatorCorrected: opCorrected,
		AvgConfidence:     avgConf,
		ByState:           byState,
		BySource:          bySource,
	}, nil
}

func (s *Store) groupCount(ctx context.Context, sql, jobID string) (map[string]int, error) {
	rows, err := s.db.Query(ctx, sql, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]int)
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
