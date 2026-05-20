package export

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"vividp/job"
)

const (
	workStream   = "VIVIDP_JOBS"
	workSubject  = "vividp.jobs.work.export"
	consumerName = "export-service"
)

// ExportPayload is the JSON structure written to the tenant destination.
type ExportPayload struct {
	JobID        string         `json:"job_id"`
	TenantID     string         `json:"tenant_id"`
	SystemID     string         `json:"system_id"`
	Filename     string         `json:"source_filename"`
	PageCount    *int           `json:"page_count,omitempty"`
	DocumentType string         `json:"document_type,omitempty"`
	Fields       []ExportField  `json:"fields"`
	ExportedAt   time.Time      `json:"exported_at"`
}

type ExportField struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Confidence  *int   `json:"confidence,omitempty"`
	ValueSource string `json:"value_source,omitempty"`
}

// Worker subscribes to the export work queue and processes one job at a time.
type Worker struct {
	id      string
	svc     *job.Service
	storage *Storage
	log     *slog.Logger
}

func NewWorker(id string, svc *job.Service, storage *Storage, log *slog.Logger) *Worker {
	return &Worker{
		id:      id,
		svc:     svc,
		storage: storage,
		log:     log.With("module", "export-worker", "worker_id", id),
	}
}

// Start creates the durable consumer and processes messages until ctx is cancelled.
func (w *Worker) Start(ctx context.Context, js jetstream.JetStream) error {
	consumer, err := js.CreateOrUpdateConsumer(ctx, workStream, jetstream.ConsumerConfig{
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: workSubject,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		MaxDeliver:    3,
		AckWait:       2 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		w.handleMessage(ctx, msg)
	})
	if err != nil {
		return fmt.Errorf("start consume: %w", err)
	}
	defer cc.Stop()

	w.log.Info("export worker ready", "subject", workSubject)
	<-ctx.Done()
	return ctx.Err()
}

func (w *Worker) handleMessage(ctx context.Context, msg jetstream.Msg) {
	var wm job.WorkMessage
	if err := json.Unmarshal(msg.Data(), &wm); err != nil {
		w.log.Warn("invalid work message", "error", err)
		msg.Nak()
		return
	}

	if err := w.processJob(ctx, wm.JobID); err != nil {
		w.log.Error("export failed", "job_id", wm.JobID, "error", err)
		msg.Nak()
		return
	}
	msg.Ack()
}

func (w *Worker) processJob(ctx context.Context, jobID string) error {
	// Claim the job — competing workers use SELECT FOR UPDATE SKIP LOCKED
	j, err := w.svc.ClaimJob(ctx, job.StatusRecognized, w.id)
	if err != nil {
		return fmt.Errorf("claim job: %w", err)
	}
	if j == nil {
		return nil // another worker claimed it
	}

	start := time.Now()
	w.log.Info("exporting job", "job_id", j.ID, "file", j.SourceFilename)

	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusExporting,
		NewState: job.StateData{"export_started": time.Now().UTC().Format(time.RFC3339)},
		WorkerID: w.id,
		Note:     "export started",
	})
	if err != nil {
		return fmt.Errorf("transition to EXPORTING: %w", err)
	}

	// Load all recognized fields for this job
	fields, err := w.loadFields(ctx, j.ID)
	if err != nil {
		return w.failJob(ctx, j.ID, fmt.Errorf("load fields: %w", err))
	}

	// Build export payload
	docType := ""
	if v, ok := j.JobState["document_type"]; ok {
		docType, _ = v.(string)
	}

	payload := ExportPayload{
		JobID:        j.ID,
		TenantID:     j.TenantID,
		SystemID:     j.SystemID,
		Filename:     j.SourceFilename,
		PageCount:    j.PageCount,
		DocumentType: docType,
		Fields:       fields,
		ExportedAt:   time.Now().UTC(),
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return w.failJob(ctx, j.ID, fmt.Errorf("marshal payload: %w", err))
	}

	// Write to jobs bucket under export/ prefix
	exportKey := fmt.Sprintf("jobs/%s/%s/%s/export/result.json", j.TenantID, j.SystemID, j.ID)
	size, err := w.storage.UploadJSON(ctx, exportKey, data)
	if err != nil {
		return w.failJob(ctx, j.ID, fmt.Errorf("upload export: %w", err))
	}

	// Record export artifact
	w.svc.RecordArtifact(ctx, job.AddArtifactRequest{
		JobID: j.ID,
		Artifact: job.Artifact{
			Key:       exportKey,
			Type:      "export_json",
			MimeType:  "application/json",
			SizeBytes: size,
			CreatedAt: time.Now().UTC(),
		},
	})

	duration := time.Since(start).Milliseconds()
	w.log.Info("export complete", "job_id", j.ID, "key", exportKey, "fields", len(fields), "ms", duration)

	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusCompleted,
		NewState: job.StateData{
			"export_key": exportKey,
			"export_ms":  duration,
		},
		WorkerID:    w.id,
		Note:        "export complete",
		StationName: "export",
		DurationMS:  duration,
	})
	return err
}

// loadFields fetches all recognized fields for a job.
func (w *Worker) loadFields(ctx context.Context, jobID string) ([]ExportField, error) {
	fields, err := w.svc.ListFields(ctx, jobID)
	if err != nil {
		return nil, err
	}

	var out []ExportField
	for _, f := range fields {
		if f.FinalValue == nil || *f.FinalValue == "" {
			continue
		}
		ef := ExportField{Name: f.FieldName, Value: *f.FinalValue}
		if f.Confidence != nil {
			ef.Confidence = f.Confidence
		}
		if f.ValueSource != nil {
			ef.ValueSource = *f.ValueSource
		}
		out = append(out, ef)
	}
	return out, nil
}

func (w *Worker) failJob(ctx context.Context, jobID string, cause error) error {
	msg := cause.Error()
	w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    jobID,
		ToStatus: job.StatusFailed,
		NewState: job.StateData{"error": msg},
		WorkerID: w.id,
		Note:     msg,
	})
	return fmt.Errorf("export failed: %w", cause)
}
