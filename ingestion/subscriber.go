// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/subscriber.go
// NATS JetStream consumer for MinIO bucket notifications.
// MinIO publishes ObjectCreated events to formstorm.minio.events when a file
// lands in the input bucket. This subscriber picks them up and feeds them into
// the same work queue as the HTTP webhook handler.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	minioStreamName = "FORMSTORM_MINIO"
	minioSubject    = "formstorm.minio.events"
	consumerDurable = "ingestion-service"
)

// Subscriber consumes MinIO bucket notifications from NATS JetStream.
type Subscriber struct {
	js          jetstream.JetStream
	workCh      chan<- DetectedFile
	cfg         Config
	accumulator *FolderAccumulator
	storage     *Storage
	log         *slog.Logger
}

func NewSubscriber(js jetstream.JetStream, workCh chan<- DetectedFile, cfg Config, accumulator *FolderAccumulator, storage *Storage, log *slog.Logger) *Subscriber {
	return &Subscriber{
		js:          js,
		workCh:      workCh,
		cfg:         cfg,
		accumulator: accumulator,
		storage:     storage,
		log:         log.With("module", "subscriber"),
	}
}

// Start ensures the FORMSTORM_MINIO stream exists, creates a durable consumer,
// and processes messages until ctx is cancelled.
func (s *Subscriber) Start(ctx context.Context) error {
	// Ensure the stream that captures MinIO notifications exists.
	// MinIO publishes to this subject via its native NATS JetStream integration.
	_, err := s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      minioStreamName,
		Subjects:  []string{minioSubject},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    1 * time.Hour, // MinIO events are ephemeral — no need for long retention
	})
	if err != nil {
		return fmt.Errorf("create minio stream: %w", err)
	}

	consumer, err := s.js.CreateOrUpdateConsumer(ctx, minioStreamName, jetstream.ConsumerConfig{
		Durable:       consumerDurable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy, // don't replay old events on restart
		MaxDeliver:    3,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create minio consumer: %w", err)
	}

	cc, err := consumer.Consume(s.handleMessage)
	if err != nil {
		return fmt.Errorf("start consume: %w", err)
	}
	defer cc.Stop()

	s.log.Info("NATS subscriber ready", "subject", minioSubject)

	<-ctx.Done()
	return ctx.Err()
}

func (s *Subscriber) handleMessage(msg jetstream.Msg) {
	var event S3Event
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		s.log.Warn("could not parse MinIO event", "error", err)
		msg.Nak()
		return
	}

	dispatched := 0
	for _, rec := range event.Records {
		if !strings.Contains(rec.EventName, "ObjectCreated") {
			continue
		}

		key := rec.S3.Object.Key
		prefix := folderPrefix(key)

		if isSignalFile(key) && prefix != "" && s.accumulator != nil {
			tenantID, systemID := extractRouting(key, s.cfg.DefaultTenantID, s.cfg.DefaultSystemID)
			bucket := rec.S3.Bucket.Name
			var metaContent string
			if filepath.Base(key) == "_READY.json" {
				if rc, err := s.storage.ReadObject(context.Background(), bucket, key); err == nil {
					if raw, err := io.ReadAll(io.LimitReader(rc, 1<<16)); err == nil {
						metaContent = string(raw)
					}
					rc.Close()
				}
			}
			detected, ok := s.accumulator.Signal(prefix, filepath.Base(prefix), metaContent)
			if ok {
				detected.Bucket = bucket
				detected.TenantID = tenantID
				detected.SystemID = systemID
				select {
				case s.workCh <- detected:
					dispatched++
					s.log.Info("folder job queued", "folder", detected.Filename, "file_count", len(detected.AllKeys))
				default:
					s.log.Warn("work queue full — nacking folder signal", "key", key)
					msg.Nak()
					return
				}
			}
			continue
		}

		if shouldIgnore(key) {
			continue
		}

		if prefix != "" && s.accumulator != nil {
			tenantID, systemID := extractRouting(key, s.cfg.DefaultTenantID, s.cfg.DefaultSystemID)
			s.accumulator.Add(prefix, DetectedFile{
				Bucket:   rec.S3.Bucket.Name,
				Key:      key,
				Filename: filepath.Base(key),
				Size:     rec.S3.Object.Size,
				TenantID: tenantID,
				SystemID: systemID,
			})
			continue
		}

		tenantID, systemID := extractRouting(key, s.cfg.DefaultTenantID, s.cfg.DefaultSystemID)
		detected := DetectedFile{
			Bucket:   rec.S3.Bucket.Name,
			Key:      key,
			Filename: filepath.Base(key),
			Size:     rec.S3.Object.Size,
			TenantID: tenantID,
			SystemID: systemID,
		}
		select {
		case s.workCh <- detected:
			dispatched++
			s.log.Info("file queued", "file", detected.Filename, "tenant", detected.TenantID, "size", detected.Size)
		default:
			s.log.Warn("work queue full — nacking", "key", key)
			msg.Nak()
			return
		}
	}

	msg.Ack()
}
