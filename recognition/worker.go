package recognition

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/nats-io/nats.go/jetstream"

	"vividp/job"
)

const (
	workStream    = "VIVIDP_JOBS"
	workSubject   = "vividp.jobs.work.recognize"
	consumerName  = "recognition-service"
	lowConfidence = 70 // below this, fall back to the more capable model
)

// Worker subscribes to the recognition work queue and processes one job at a time.
type Worker struct {
	id      string
	svc     *job.Service
	storage *Storage
	cfg     Config
	client  anthropic.Client
	log     *slog.Logger
}

func NewWorker(id string, svc *job.Service, storage *Storage, cfg Config, log *slog.Logger) *Worker {
	return &Worker{
		id:      id,
		svc:     svc,
		storage: storage,
		cfg:     cfg,
		client:  anthropic.NewClient(), // reads ANTHROPIC_API_KEY from env
		log:     log.With("module", "recognition-worker", "worker_id", id),
	}
}

// Start creates the durable consumer and begins processing messages.
func (w *Worker) Start(ctx context.Context, js jetstream.JetStream) error {
	consumer, err := js.CreateOrUpdateConsumer(ctx, workStream, jetstream.ConsumerConfig{
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: workSubject,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		MaxDeliver:    3,
		AckWait:       5 * time.Minute,
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

	w.log.Info("recognition worker ready", "subject", workSubject)
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
		w.log.Error("recognition failed", "job_id", wm.JobID, "error", err)
		msg.Nak()
		return
	}
	msg.Ack()
}

func (w *Worker) processJob(ctx context.Context, jobID string) error {
	j, err := w.svc.ClaimJob(ctx, job.StatusIngested, w.id)
	if err != nil {
		return fmt.Errorf("claim job: %w", err)
	}
	if j == nil {
		return nil // another worker claimed it
	}

	start := time.Now()
	w.log.Info("recognizing job", "job_id", j.ID, "file", j.SourceFilename)

	j, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusRecognizing,
		NewState: job.StateData{"recognition_started": time.Now().UTC().Format(time.RFC3339)},
		WorkerID: w.id,
		Note:     "recognition started",
	})
	if err != nil {
		return fmt.Errorf("transition to RECOGNIZING: %w", err)
	}

	// Collect page JPEG artifacts (sorted by page_num ascending)
	var pageKeys []string
	for _, a := range j.Artifacts {
		if a.Type == "page_jpeg" {
			pageKeys = append(pageKeys, a.Key)
		}
	}
	if len(pageKeys) == 0 {
		return w.failJob(ctx, j.ID, "no page_jpeg artifacts found")
	}

	// Build content blocks: images then text prompt
	var blocks []anthropic.ContentBlockParamUnion
	for _, key := range pageKeys {
		data, err := w.storage.ReadAll(ctx, key)
		if err != nil {
			return w.failJobErr(ctx, j.ID, fmt.Errorf("download page %s: %w", key, err))
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		blocks = append(blocks, anthropic.NewImageBlockBase64("image/jpeg", encoded))
	}
	blocks = append(blocks, anthropic.NewTextBlock(
		"Analyze the document shown in the image(s) above. "+
			"Use the extract_invoice tool to return structured data. "+
			"Extract all visible fields. For fields you cannot read clearly, omit them rather than guessing.",
	))

	// First attempt with default model
	fields, model, err := w.callClaude(ctx, blocks, w.cfg.DefaultModel)
	if err != nil {
		return w.failJobErr(ctx, j.ID, err)
	}

	// Retry with fallback model if confidence is low
	if fields.Confidence < lowConfidence && w.cfg.FallbackModel != w.cfg.DefaultModel {
		w.log.Info("low confidence — retrying with fallback model",
			"job_id", j.ID, "confidence", fields.Confidence, "model", w.cfg.FallbackModel)
		fields, model, err = w.callClaude(ctx, blocks, w.cfg.FallbackModel)
		if err != nil {
			return w.failJobErr(ctx, j.ID, err)
		}
	}

	if err := w.writeFields(ctx, j, fields, model); err != nil {
		return w.failJobErr(ctx, j.ID, fmt.Errorf("write fields: %w", err))
	}

	duration := time.Since(start).Milliseconds()
	w.log.Info("recognition complete", "job_id", j.ID,
		"document_type", fields.DocumentType, "confidence", fields.Confidence,
		"model", model, "ms", duration)

	_, err = w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusRecognized,
		NewState: job.StateData{
			"document_type":     fields.DocumentType,
			"confidence":        fields.Confidence,
			"recognition_model": model,
			"recognition_ms":    duration,
		},
		WorkerID:    w.id,
		Note:        fmt.Sprintf("recognized as %s (confidence %d%%)", fields.DocumentType, fields.Confidence),
		StationName: "recognize",
		DurationMS:  duration,
	})
	return err
}

// callClaude sends the image blocks to Claude and returns parsed InvoiceFields.
func (w *Worker) callClaude(ctx context.Context, blocks []anthropic.ContentBlockParamUnion, model string) (*InvoiceFields, string, error) {
	msg, err := w.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 4096,
		Tools: []anthropic.ToolUnionParam{
			anthropic.ToolUnionParamOfTool(
				anthropic.ToolInputSchemaParam{Properties: invoiceToolSchema()},
				invoiceToolName,
			),
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool(invoiceToolName),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(blocks...),
		},
	})
	if err != nil {
		return nil, model, fmt.Errorf("claude API call: %w", err)
	}

	for _, block := range msg.Content {
		if tu := block.AsToolUse(); tu.Name == invoiceToolName {
			var fields InvoiceFields
			if err := json.Unmarshal(tu.Input, &fields); err != nil {
				return nil, model, fmt.Errorf("parse tool result: %w", err)
			}
			return &fields, model, nil
		}
	}

	return nil, model, fmt.Errorf("claude did not call the expected tool")
}

// writeFields persists all recognized fields to job_fields.
func (w *Worker) writeFields(ctx context.Context, j *job.Job, fields *InvoiceFields, model string) error {
	type fieldDef struct {
		name  string
		value string
	}

	defs := []fieldDef{
		{"document_type", fields.DocumentType},
		{"invoice_number", fields.InvoiceNumber},
		{"invoice_date", fields.InvoiceDate},
		{"due_date", fields.DueDate},
		{"purchase_order", fields.PurchaseOrder},
		{"vendor_name", fields.VendorName},
		{"vendor_address", fields.VendorAddress},
		{"vendor_vat", fields.VendorVAT},
		{"customer_name", fields.CustomerName},
		{"customer_address", fields.CustomerAddress},
		{"customer_vat", fields.CustomerVAT},
		{"subtotal", fields.Subtotal},
		{"tax_amount", fields.TaxAmount},
		{"tax_rate", fields.TaxRate},
		{"total_amount", fields.TotalAmount},
		{"currency", fields.Currency},
		{"bank_account", fields.BankAccount},
		{"iban", fields.IBAN},
		{"bic", fields.BIC},
		{"payment_terms", fields.PaymentTerms},
	}

	confidence := fields.Confidence
	source := "llm"
	state := "recognized"
	fieldOrder := 0

	for _, fd := range defs {
		if fd.value == "" {
			continue
		}
		v := fd.value
		recJSON, _ := json.Marshal(map[string]any{
			"llm": map[string]any{
				"model":      model,
				"raw_value":  fd.value,
				"confidence": confidence,
			},
		})
		fieldOrder++
		f := &job.Field{
			JobID:       j.ID,
			FieldName:   fd.name,
			IsJobLevel:  true,
			FinalValue:  &v,
			FieldState:  &state,
			ValueSource: &source,
			Confidence:  &confidence,
			FieldOrder:  fieldOrder,
			IsVisible:   true,
			Recognition: recJSON,
		}
		if err := w.svc.CreateField(ctx, f); err != nil {
			w.log.Warn("failed to write field", "job_id", j.ID, "field", fd.name, "error", err)
		}
	}

	if len(fields.LineItems) > 0 {
		lineJSON, _ := json.Marshal(fields.LineItems)
		w.svc.MergeJobState(ctx, j.ID, job.StateData{"line_items": string(lineJSON)})
	}

	return nil
}

func (w *Worker) failJob(ctx context.Context, jobID, msg string) error {
	w.svc.Transition(ctx, job.TransitionRequest{
		JobID:    jobID,
		ToStatus: job.StatusFailed,
		NewState: job.StateData{"error": msg},
		WorkerID: w.id,
		Note:     msg,
	})
	return fmt.Errorf("recognition failed: %s", msg)
}

func (w *Worker) failJobErr(ctx context.Context, jobID string, err error) error {
	return w.failJob(ctx, jobID, err.Error())
}
