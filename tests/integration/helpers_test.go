package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"vividp/export"
	"vividp/ingestion"
	"vividp/job"
	"vividp/license"
	"vividp/logger"
	"vividp/recognition"
)

// ── Infrastructure defaults (match docker-compose dev config) ─────────────────

const (
	testDatabaseURL      = "postgres://vividp:vividp_dev@localhost:5432/vividp"
	testNatsURL          = "nats://localhost:4222"
	testStorageEndpoint  = "localhost:9000"
	testStorageAccessKey = "vividp"
	testStorageSecretKey = "vividp_dev"
	testInputBucket      = "input"
	testJobsBucket       = "jobs"
	testConversionURL    = "http://localhost:8082"
	testTenantID         = "00000000-0000-0000-0000-000000000001"
	testSystemID         = "00000000-0000-0000-0000-000000000002"

	// Location of test PDF fixtures relative to repo root
	testFilesBase = "../../test_input_files/" + testTenantID + "/" + testSystemID
)

// testInfra holds live connections for the duration of a test.
type testInfra struct {
	db    *pgxpool.Pool
	nc    *nats.Conn
	js    jetstream.JetStream
	mc    *minio.Client
	svc   *job.Service
	store *job.Store
	log   *slog.Logger
}

// TestMain checks that all required infrastructure is reachable before running
// any tests. Missing infra or a missing ANTHROPIC_API_KEY skips the entire suite
// rather than failing — the tests are designed to run alongside docker compose,
// not as isolated unit tests.
func TestMain(m *testing.M) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("SKIP: ANTHROPIC_API_KEY not set — skipping integration tests")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// PostgreSQL
	db, err := pgxpool.New(ctx, testDatabaseURL)
	if err != nil || db.Ping(ctx) != nil {
		fmt.Println("SKIP: PostgreSQL not reachable — skipping integration tests")
		os.Exit(0)
	}
	db.Close()

	// NATS
	nc, err := nats.Connect(testNatsURL, nats.Timeout(5*time.Second))
	if err != nil {
		fmt.Println("SKIP: NATS not reachable — skipping integration tests")
		os.Exit(0)
	}
	nc.Close()

	// MinIO
	mc, err := minio.New(testStorageEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(testStorageAccessKey, testStorageSecretKey, ""),
	})
	if err != nil {
		fmt.Println("SKIP: MinIO client init failed — skipping integration tests")
		os.Exit(0)
	}
	_, err = mc.ListBuckets(ctx)
	if err != nil {
		fmt.Println("SKIP: MinIO not reachable — skipping integration tests")
		os.Exit(0)
	}

	// Conversion service
	resp, err := http.Get(testConversionURL + "/healthz")
	if err != nil || resp.StatusCode >= 500 {
		fmt.Println("SKIP: Conversion service not reachable at", testConversionURL, "— skipping integration tests")
		os.Exit(0)
	}

	os.Exit(m.Run())
}

// connect opens all infrastructure connections and wires up the job service.
// The caller is responsible for closing them (use defer infra.close(t)).
func connect(t *testing.T) *testInfra {
	t.Helper()
	ctx := context.Background()

	log, logCleanup, err := logger.Setup("info", "")
	require.NoError(t, err)
	t.Cleanup(logCleanup)

	db, err := pgxpool.New(ctx, testDatabaseURL)
	require.NoError(t, err, "connect postgres")
	require.NoError(t, db.Ping(ctx), "ping postgres")

	nc, err := nats.Connect(testNatsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(3),
		nats.ReconnectWait(time.Second),
	)
	require.NoError(t, err, "connect NATS")

	js, err := jetstream.New(nc)
	require.NoError(t, err, "jetstream init")

	mc, err := minio.New(testStorageEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(testStorageAccessKey, testStorageSecretKey, ""),
	})
	require.NoError(t, err, "minio client")

	store := job.NewStore(db)
	publisher, err := job.NewPublisher(nc)
	require.NoError(t, err, "job publisher")
	svc := job.NewService(store, publisher, log)

	t.Cleanup(func() {
		nc.Close()
		db.Close()
	})

	return &testInfra{
		db:    db,
		nc:    nc,
		js:    js,
		mc:    mc,
		svc:   svc,
		store: store,
		log:   log,
	}
}

// buildIngestionWorker creates an ingestion Worker pointed at the docker-compose infra.
func (ti *testInfra) buildIngestionWorker(t *testing.T, id string) *ingestion.Worker {
	t.Helper()
	cfg := ingestion.LoadConfig()
	cfg.ConversionURL = testConversionURL
	cfg.DefaultTenantID = testTenantID
	cfg.DefaultSystemID = testSystemID

	storage, err := ingestion.NewStorage(cfg)
	require.NoError(t, err, "ingestion storage")

	converter := ingestion.NewConversionClient(cfg.ConversionURL)
	return ingestion.NewWorker(id, ti.svc, storage, converter, cfg, license.AlwaysGranted{}, ti.log)
}

// buildRecognitionWorker creates a recognition Worker pointed at the docker-compose infra.
func (ti *testInfra) buildRecognitionWorker(t *testing.T, id string) *recognition.Worker {
	t.Helper()
	cfg := recognition.LoadConfig()
	cfg.StorageEndpoint = testStorageEndpoint
	cfg.StorageAccessKey = testStorageAccessKey
	cfg.StorageSecretKey = testStorageSecretKey

	storage, err := recognition.NewStorage(cfg)
	require.NoError(t, err, "recognition storage")

	return recognition.NewWorker(id, ti.svc, storage, cfg, ti.log)
}

// buildExportWorker creates an export Worker pointed at the docker-compose infra.
func (ti *testInfra) buildExportWorker(t *testing.T, id string) *export.Worker {
	t.Helper()
	cfg := export.LoadConfig()
	cfg.StorageEndpoint = testStorageEndpoint
	cfg.StorageAccessKey = testStorageAccessKey
	cfg.StorageSecretKey = testStorageSecretKey

	storage, err := export.NewStorage(cfg)
	require.NoError(t, err, "export storage")

	return export.NewWorker(id, ti.svc, storage, ti.log)
}

// uploadTestFile uploads a local file from test_input_files/ to MinIO.
func (ti *testInfra) uploadTestFile(t *testing.T, localRelPath, bucket, destKey string) {
	t.Helper()
	data, err := os.ReadFile(localRelPath)
	require.NoError(t, err, "read test file %s", localRelPath)

	_, err = ti.mc.PutObject(
		context.Background(),
		bucket,
		destKey,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	require.NoError(t, err, "upload test file to minio key=%s", destKey)
}

// findJobBySourceKey returns the job ID for a job created from the given source key.
func (ti *testInfra) findJobBySourceKey(t *testing.T, bucket, key string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var id string
	err := ti.db.QueryRow(ctx,
		`SELECT id FROM jobs WHERE source_bucket = $1 AND source_key = $2 LIMIT 1`,
		bucket, key,
	).Scan(&id)
	require.NoError(t, err, "find job by source_key bucket=%s key=%s", bucket, key)
	return id
}

// waitForStatus polls the DB until jobID reaches the wanted status or the
// deadline expires. Returns the job at the expected status.
func (ti *testInfra) waitForStatus(t *testing.T, jobID string, want job.Status, timeout time.Duration) *job.Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		j, err := ti.svc.GetJob(context.Background(), jobID)
		require.NoError(t, err, "poll job %s", jobID)
		if j.Status == want {
			return j
		}
		if j.Status == job.StatusFailed || j.Status == job.StatusDeadLetter {
			t.Fatalf("job %s reached terminal failure status %s while waiting for %s", jobID, j.Status, want)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for job %s to reach %s (last seen: check DB)", jobID, want)
	return nil
}

// deleteMinIOPrefix removes all objects under a prefix in the jobs bucket.
func (ti *testInfra) deleteMinIOPrefix(t *testing.T, prefix string) {
	t.Helper()
	ctx := context.Background()
	objectsCh := ti.mc.ListObjects(ctx, testJobsBucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	for obj := range objectsCh {
		if obj.Err != nil {
			continue
		}
		_ = ti.mc.RemoveObject(ctx, testJobsBucket, obj.Key, minio.RemoveObjectOptions{})
	}
}

// cleanupJob hard-deletes a job row (cascade to all child rows) and removes its MinIO artifacts.
func (ti *testInfra) cleanupJob(t *testing.T, jobID string) {
	t.Helper()
	if jobID == "" {
		return
	}
	j, err := ti.svc.GetJob(context.Background(), jobID)
	if err == nil {
		ti.deleteMinIOPrefix(t, fmt.Sprintf("jobs/%s/%s/%s", j.TenantID, j.SystemID, jobID))
	}
	_ = ti.svc.DeleteJob(context.Background(), jobID)
}

// cleanupInputKey removes a key from the input bucket (uploaded by the test).
func (ti *testInfra) cleanupInputKey(t *testing.T, key string) {
	t.Helper()
	_ = ti.mc.RemoveObject(context.Background(), testInputBucket, key, minio.RemoveObjectOptions{})
}
