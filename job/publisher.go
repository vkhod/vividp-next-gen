// ═══════════════════════════════════════════════════════════════════════════════
// job/publisher.go
// NATS JetStream — publishes events and work messages after every DB commit.
// ═══════════════════════════════════════════════════════════════════════════════
package job

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const streamName = "FORMSTORM_JOBS"

// Event is published to formstorm.jobs.events.{STATUS} after every transition.
// Kept lean — consumers fetch full job details from the Job Service if needed.
type Event struct {
	JobID      string    `json:"job_id"`
	TenantID   string    `json:"tenant_id"`
	SystemID   string    `json:"system_id"`
	Status     Status    `json:"status"`
	PageCount  *int      `json:"page_count,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// WorkMessage is published to formstorm.jobs.work.{station} to trigger the
// next pipeline station. Carries only routing context — not file data.
type WorkMessage struct {
	JobID     string `json:"job_id"`
	TenantID  string `json:"tenant_id"`
	SystemID  string `json:"system_id"`
	PageCount *int   `json:"page_count,omitempty"`
}

// Publisher manages NATS JetStream publishing for the Job Service.
type Publisher struct {
	js jetstream.JetStream
}

// NewPublisher connects to JetStream and ensures the FORMSTORM_JOBS stream exists.
// Idempotent — safe to call on every startup.
func NewPublisher(nc *nats.Conn) (*Publisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream init: %w", err)
	}

	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name: streamName,
		Subjects: []string{
			"formstorm.jobs.events.>", // broadcast — Admin Portal, Temporal, Audit
			"formstorm.jobs.work.>",   // task assignment — one worker pool per station
		},
		Storage:   jetstream.FileStorage,           // survive restarts
		Retention: jetstream.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,             // 30-day retention
		Replicas:  1,                                // increase to 3 for clustered on-prem
	})
	if err != nil {
		return nil, fmt.Errorf("create stream: %w", err)
	}

	return &Publisher{js: js}, nil
}

// PublishTransition publishes both the broadcast event AND the next-station
// work message (if applicable) for a job state transition.
// Called by the Service AFTER the PostgreSQL commit succeeds.
// If publish fails, Temporal will detect the stuck job and retry.
func (p *Publisher) PublishTransition(ctx context.Context, j *Job) error {
	// 1. Broadcast event — anyone subscribed to events.> receives this
	event := Event{
		JobID:      j.ID,
		TenantID:   j.TenantID,
		SystemID:   j.SystemID,
		Status:     j.Status,
		PageCount:  j.PageCount,
		OccurredAt: time.Now().UTC(),
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	eventSubject := fmt.Sprintf("formstorm.jobs.events.%s", string(j.Status))
	if _, err = p.js.Publish(ctx, eventSubject, eventJSON); err != nil {
		return fmt.Errorf("publish event %s: %w", eventSubject, err)
	}

	// 2. Work message — only if this status hands off to a downstream station
	if nextStation, ok := NextWorkSubject(j.Status); ok {
		work := WorkMessage{
			JobID:     j.ID,
			TenantID:  j.TenantID,
			SystemID:  j.SystemID,
			PageCount: j.PageCount,
		}
		workJSON, _ := json.Marshal(work)
		workSubject := fmt.Sprintf("formstorm.jobs.work.%s", nextStation)

		if _, err = p.js.Publish(ctx, workSubject, workJSON); err != nil {
			return fmt.Errorf("publish work %s: %w", workSubject, err)
		}
	}

	return nil
}
