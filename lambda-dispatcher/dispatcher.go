package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"vividp/job"
)

const (
	eventStream   = "VIVIDP_JOBS"
	eventSubject  = "vividp.jobs.events.>"
	consumerName  = "lambda-dispatcher"
	maxDeliver    = 3
	ackWait       = time.Minute
)

// Dispatcher subscribes to vividp.jobs.events.* and forwards matching events
// to Lambda Function URLs via HTTP POST. Entirely config-driven — no code
// changes needed when adding a new Lambda trigger.
type Dispatcher struct {
	js            jetstream.JetStream
	dispatchTable map[job.Status]string
	httpClient    *http.Client
	log           *slog.Logger
}

// NewDispatcher creates a Dispatcher with the given dispatch table and HTTP timeout.
func NewDispatcher(js jetstream.JetStream, table map[job.Status]string, timeout time.Duration, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		js:            js,
		dispatchTable: table,
		httpClient:    &http.Client{Timeout: timeout},
		log:           log.With("module", "lambda-dispatcher"),
	}
}

// Run ensures the VIVIDP_JOBS stream exists, then creates a durable consumer
// on vividp.jobs.events.> and processes messages until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	_, err := d.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      eventStream,
		Subjects:  []string{"vividp.jobs.events.>", "vividp.jobs.work.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
		Replicas:  1,
	})
	if err != nil {
		return fmt.Errorf("ensure stream: %w", err)
	}

	consumer, err := d.js.CreateOrUpdateConsumer(ctx, eventStream, jetstream.ConsumerConfig{
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: eventSubject,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		MaxDeliver:    maxDeliver,
		AckWait:       ackWait,
	})
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		d.handleMessage(ctx, msg)
	})
	if err != nil {
		return fmt.Errorf("start consume: %w", err)
	}
	defer cc.Stop()

	d.log.Info("dispatcher running", "dispatch_entries", len(d.dispatchTable))
	<-ctx.Done()
	return nil
}

func (d *Dispatcher) handleMessage(ctx context.Context, msg jetstream.Msg) {
	var event job.Event
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		d.log.Error("unmarshal event", "error", err)
		_ = msg.Nak()
		return
	}

	url, ok := d.dispatchTable[event.Status]
	if !ok {
		// No Lambda configured for this status — silently ack and move on
		_ = msg.Ack()
		return
	}

	if err := d.invoke(ctx, url, msg.Data()); err != nil {
		d.log.Error("lambda invocation failed",
			"job_id", event.JobID,
			"status", event.Status,
			"url", url,
			"error", err,
		)
		_ = msg.Nak()
		return
	}

	d.log.Info("lambda invoked",
		"job_id", event.JobID,
		"status", event.Status,
		"url", url,
	)
	_ = msg.Ack()
}

// invoke HTTP POSTs the raw event JSON to the Lambda Function URL.
// Returns an error if the Lambda returns a non-2xx response.
func (d *Dispatcher) invoke(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lambda returned HTTP %d", resp.StatusCode)
	}
	return nil
}
