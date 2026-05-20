package job

import (
	"encoding/json"
	"time"
)

// ── State machine ─────────────────────────────────────────────────────────────

type Status string

const (
	StatusDetected         Status = "DETECTED"
	StatusIngesting        Status = "INGESTING"
	StatusConverting       Status = "CONVERTING"
	StatusIngested         Status = "INGESTED"
	StatusClassifying      Status = "CLASSIFYING"
	StatusClassified       Status = "CLASSIFIED"
	StatusRecognizing      Status = "RECOGNIZING"
	StatusRecognized       Status = "RECOGNIZED"
	StatusValidating       Status = "VALIDATING"
	StatusValidated        Status = "VALIDATED"
	StatusValidationFailed Status = "VALIDATION_FAILED"
	StatusVerifying        Status = "VERIFYING"
	StatusVerified         Status = "VERIFIED"
	StatusExporting        Status = "EXPORTING"
	StatusCompleted        Status = "COMPLETED"
	StatusFailed           Status = "FAILED"
	StatusDeadLetter       Status = "DEAD_LETTER"
)

// legalTransitions enforces the state machine.
// A station cannot skip states or go backwards.
var legalTransitions = map[Status][]Status{
	StatusDetected:         {StatusIngesting, StatusFailed},
	StatusIngesting:        {StatusConverting, StatusFailed},
	StatusConverting:       {StatusIngested, StatusFailed},
	// INGESTED can go to CLASSIFYING (full pipeline) or RECOGNIZING (LLM-first, skip classification)
	StatusIngested:         {StatusClassifying, StatusRecognizing, StatusFailed},
	StatusClassifying:      {StatusClassified, StatusFailed},
	StatusClassified:       {StatusRecognizing, StatusFailed},
	StatusRecognizing:      {StatusRecognized, StatusFailed},
	// RECOGNIZED can go to VALIDATING (full pipeline) or EXPORTING (skip validation)
	StatusRecognized:       {StatusValidating, StatusExporting, StatusFailed},
	StatusValidating:       {StatusValidated, StatusValidationFailed, StatusFailed},
	StatusValidated:        {StatusExporting, StatusFailed},
	StatusValidationFailed: {StatusVerifying, StatusFailed},
	StatusVerifying:        {StatusVerified, StatusFailed},
	StatusVerified:         {StatusExporting, StatusFailed},
	StatusExporting:        {StatusCompleted, StatusFailed},
	StatusFailed:           {StatusDetected, StatusDeadLetter},
}

func IsLegalTransition(from, to Status) bool {
	for _, s := range legalTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// nextWorkStation maps a job status to the NATS work subject for the next station.
// Only statuses that hand off to a downstream worker are listed.
var nextWorkStation = map[Status]string{
	// LLM-first pipeline: INGESTED → recognize (classification merged into recognition)
	StatusIngested:         "recognize",
	StatusClassified:       "recognize",
	// LLM-first pipeline: RECOGNIZED → export (validation deferred)
	StatusRecognized:       "export",
	StatusValidated:        "export",
	StatusValidationFailed: "verify",
	StatusVerified:         "export",
}

func NextWorkSubject(s Status) (string, bool) {
	sub, ok := nextWorkStation[s]
	return sub, ok
}

// ── JSONB value types ─────────────────────────────────────────────────────────

// StateData is the evolving JSONB pipeline accumulator.
// Each station merges its output using PostgreSQL || operator.
type StateData map[string]any

// Classification is one ranked document type candidate.
// Maps to jcn1/jct1/jcc1 pattern from legacy XML.
type Classification struct {
	Rank       int    `json:"rank"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	Confidence int    `json:"confidence"`
}

// Artifact is one file stored in MinIO/S3.
type Artifact struct {
	Key       string    `json:"key"`
	Type      string    `json:"type"`               // "original" | "page_jpeg" | "meta_json"
	MimeType  string    `json:"mime_type,omitempty"` // "application/pdf" | "image/jpeg" | ...
	PageNum   int       `json:"page_num"`            // 0 = job-level artifact
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ── Job ───────────────────────────────────────────────────────────────────────

// Job is the central domain object. One row in the jobs table.
// Pointer types (*string, *int, *time.Time) map to nullable PostgreSQL columns.
type Job struct {
	// Identity
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	SystemID    string  `json:"system_id"` // FK → systems table

	// Naming (two distinct concepts from Delphi)
	JobName  *string `json:"job_name,omitempty"`  // ARJob.JobFileName — file path
	JobAlias *string `json:"job_alias,omitempty"` // ARJob.JobName — display name

	// State machine
	Status Status `json:"status"`
	Stage  string `json:"stage"`

	// Capture
	CaptureType      *int `json:"capture_type,omitempty"`
	MetadataOverride bool `json:"metadata_override"`

	// Source file
	SourceFilename   string  `json:"source_filename"`
	SourceBucket     string  `json:"source_bucket"`
	SourceKey        string  `json:"source_key"`
	SizeBytes        int64   `json:"size_bytes"`
	OriginalFilePath *string `json:"original_file_path,omitempty"`
	NFiles           int     `json:"nfiles"` // number of source files

	// Counts
	PageCount            *int `json:"page_count,omitempty"`
	ScannedPages         *int `json:"scanned_pages,omitempty"`
	SuspectedFieldsCount *int `json:"suspected_fields_count,omitempty"`

	// Queue management
	Priority int     `json:"priority"` // higher = processed first
	OnHold   bool    `json:"on_hold"`  // operator-set pause
	OnError  int     `json:"on_error"` // 0=retry 1=skip 2=escalate
	Filter   *string `json:"filter,omitempty"`
	UserData *string `json:"user_data,omitempty"` // ARJob.JobUserData

	// Flags (from Delphi — important for analytics)
	SkippedVerify    bool `json:"skipped_verify"`    // verification was bypassed
	SkippedTruTypist bool `json:"skipped_trutypist"` // typist entry was skipped
	IsDuplicate      bool `json:"is_duplicate"`
	IsArchived       bool `json:"is_archived"`

	// Export paths
	ExportDataPath  *string `json:"export_data_path,omitempty"`
	ExportImagePath *string `json:"export_image_path,omitempty"`
	TifFileKey      *string `json:"tif_file_key,omitempty"`     // MinIO key for original TIF
	TifRegFileKey   *string `json:"tif_reg_file_key,omitempty"` // MinIO key for registered TIF

	// Verifier metrics (v_name, v_time, nksv from XML)
	VerifierName        *string `json:"verifier_name,omitempty"`
	VerificationSeconds *int    `json:"verification_seconds,omitempty"`
	KeystrokesCount     *int    `json:"keystrokes_count,omitempty"`

	// Six phase timestamps — independently nullable
	CaptureBeganAt      *time.Time `json:"capture_began_at,omitempty"`
	CaptureEndedAt      *time.Time `json:"capture_ended_at,omitempty"`
	OCRBeganAt          *time.Time `json:"ocr_began_at,omitempty"`
	OCREndedAt          *time.Time `json:"ocr_ended_at,omitempty"`
	VerificationBeganAt *time.Time `json:"verification_began_at,omitempty"`
	VerificationEndedAt *time.Time `json:"verification_ended_at,omitempty"`

	// Error info (two separate fields from Delphi)
	LastError    *string `json:"last_error,omitempty"`    // technical exception string
	ErrorComment *string `json:"error_comment,omitempty"` // human explanation

	// Integrity (__meta pattern from TJSBase)
	ContentHash    *string    `json:"content_hash,omitempty"`
	HashComputedAt *time.Time `json:"hash_computed_at,omitempty"`

	// JSONB accumulators
	Classifications []Classification `json:"classifications"`
	JobState        StateData        `json:"job_state"`
	Artifacts       []Artifact       `json:"artifacts"`
	PipelineTimings map[string]int64 `json:"pipeline_timings"` // station_name → ms

	// Worker claim
	RetryCount int        `json:"retry_count"`
	ClaimedBy  *string    `json:"claimed_by,omitempty"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ── Document ──────────────────────────────────────────────────────────────────

// Document is a logical document within a batch job.
// Replaces rpginfo end_doc/bundle boolean flags.
// A batch of 3 invoices = 3 Document rows in one Job.
type Document struct {
	ID                    string    `json:"id"`
	JobID                 string    `json:"job_id"`
	DocumentIndex         int       `json:"document_index"`          // current order (mutable)
	OriginalDocumentIndex int       `json:"original_document_index"` // immutable
	FormName              *string   `json:"form_name,omitempty"`
	BundleName            *string   `json:"bundle_name,omitempty"`
	PageCount             int       `json:"page_count"`  // maintained by trigger
	IsComplete            bool      `json:"is_complete"`
	CreatedAt             time.Time `json:"created_at"`
}

// ── Page ──────────────────────────────────────────────────────────────────────

// Page is one page within a job.
// order_key FLOAT8 enables reordering without cascade updates.
// operator_rotation is VerifyRotate from Delphi — NOT scan rotation.
type Page struct {
	ID         int64   `json:"id"`
	JobID      string  `json:"job_id"`
	DocumentID *string `json:"document_id,omitempty"`

	// Position — OrderKey is the sort key, OriginalPageIndex is immutable
	OriginalPageIndex int     `json:"original_page_index"` // immutable after ingestion
	SourcePageNumber  *int    `json:"source_page_number,omitempty"`
	OrderKey          float64 `json:"order_key"` // ORDER BY this for display sequence

	// State
	State     *string `json:"state,omitempty"`
	IsDeleted bool    `json:"is_deleted"`

	// Page role in batch
	IsSeparator   bool `json:"is_separator"`
	IsAttached    bool `json:"is_attached"`
	IsLastInForm  bool `json:"is_last_in_form"` // bLastInForm from Delphi
	ColorBarCheck int  `json:"color_bar_check"`
	WasNewBatch   bool `json:"was_new_batch"`

	// Rotation applied by operator IN the Verification Workstation
	// This is VerifyRotate from Delphi — completely separate from scan rotation
	OperatorRotation int `json:"operator_rotation"`

	// Image geometry — CRITICAL for Verification Workstation bbox rendering
	ImageHeightPx *int `json:"image_height_px,omitempty"`
	ImageWidthPx  *int `json:"image_width_px,omitempty"`

	// File references
	OriginalFilePath    *string `json:"original_file_path,omitempty"`
	ReassignedFormName  *string `json:"reassigned_form_name,omitempty"`
	ReassignedFormPage  *int    `json:"reassigned_form_page,omitempty"`
	ExportDataFilename  *string `json:"export_data_filename,omitempty"`
	ExportImageFilename *string `json:"export_image_filename,omitempty"`
	ExceptMessage       *string `json:"except_message,omitempty"`

	// FSA archive originals — state before post-processing
	FSAForm           *string `json:"fsa_form,omitempty"`
	FSAOriginalPageNo *int    `json:"fsa_original_page_no,omitempty"`
	FSAOriginalState  *string `json:"fsa_original_state,omitempty"`

	// CMC7 / MICR — banking/cheque documents
	CMC7Flags int     `json:"cmc7_flags"`
	CMC7      *string `json:"cmc7,omitempty"`

	// JSONB — stored as raw JSON, populated by preprocessing stage
	Preprocessing json.RawMessage `json:"preprocessing,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// ── Field ─────────────────────────────────────────────────────────────────────

// Field is one recognized field value within a page (or job-level).
// Hot columns (final_value, field_state, confidence) are relational for
// fast Verification Workstation queries.
// Engine details and geometry live in JSONB.
type Field struct {
	ID         int64  `json:"id"`
	JobID      string `json:"job_id"`
	PageID     *int64 `json:"page_id,omitempty"` // NULL for job-level (rjobpage) fields

	FieldName  string  `json:"field_name"`
	ArrayIndex *int    `json:"array_index,omitempty"` // 1-based for repeating fields (line items)
	SetupTable *string `json:"setup_table,omitempty"` // ARField.SetupField.SetupTable.Name
	IsJobLevel bool    `json:"is_job_level"`

	// Hot columns — relational for Verification WS queries
	FinalValue  *string `json:"final_value,omitempty"`  // typist_content
	FieldState  *string `json:"field_state,omitempty"`  // recognized|validated|not_validated
	ValueSource *string `json:"value_source,omitempty"` // ocr|llm|operator|default
	Confidence  *int    `json:"confidence,omitempty"`   // 0-100 from OCR engine

	// UI display properties — queried by Verification WS to build field list
	FieldOrder int  `json:"field_order"` // SetupField.FieldOrder
	IsVisible  bool `json:"is_visible"`  // NOT DisplayIf=DI_NEVER
	IsReadonly bool `json:"is_readonly"` // bReadOnly

	// JSONB — engine outputs, geometry, setup metadata
	Recognition json.RawMessage `json:"recognition,omitempty"` // {"ocr":{...},"llm":{...},"operator":{...}}
	Geometry    json.RawMessage `json:"geometry,omitempty"`    // {"image":{bbox},"template":{bbox}}
	SetupInfo   json.RawMessage `json:"setup_info,omitempty"`  // {"description":"...","field_type":"...","html_id":"...","dictionary":"..."}

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ── Request types ─────────────────────────────────────────────────────────────

// CreateJobRequest is the input for creating a new job at status=DETECTED.
type CreateJobRequest struct {
	TenantID         string
	SystemID         string
	Filename         string
	Bucket           string
	Key              string
	SizeBytes        int64
	OriginalFilePath *string // optional: original Windows/Linux path
	NFiles           int     // number of source files (default 1)
}

// TransitionRequest is the input for advancing a job through the state machine.
type TransitionRequest struct {
	JobID        string
	ToStatus     Status
	NewState     StateData // merged into job_state JSONB via ||
	WorkerID     string
	Note         string
	StationName  string // records timing in pipeline_timings
	DurationMS   int64
}

// AddArtifactRequest is used to append one artifact to the job manifest.
type AddArtifactRequest struct {
	JobID    string
	Artifact Artifact
}
