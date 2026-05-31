// Package integration_test contains end-to-end tests for the VividP pipeline.
//
// Prerequisites:
//   - docker compose up -d  (starts postgres, nats, minio, conversion)
//   - ANTHROPIC_API_KEY must be set in the environment
//
// Run:
//
//	ANTHROPIC_API_KEY=sk-ant-... go test ./tests/integration/... -v -timeout 300s -count=1
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vividp/ingestion"
	"vividp/job"
)

// ── TestJobStore_StateTransitions ────────────────────────────────────────────
//
// Validates the state machine enforced by job.Store without touching NATS,
// MinIO, or the conversion service. Runs in ~1s on postgres alone.

func TestJobStore_StateTransitions(t *testing.T) {
	ti := connect(t)

	ctx := context.Background()

	// Create a job at DETECTED
	j, err := ti.svc.CreateJob(ctx, job.CreateJobRequest{
		TenantID:  testTenantID,
		SystemID:  testSystemID,
		Filename:  "state-machine-test.pdf",
		Bucket:    testInputBucket,
		Key:       fmt.Sprintf("state-machine-test-%d.pdf", time.Now().UnixNano()),
		SizeBytes: 1024,
	})
	require.NoError(t, err, "create job")
	t.Cleanup(func() { ti.cleanupJob(t, j.ID) })

	assert.Equal(t, job.StatusDetected, j.Status)

	// Walk DETECTED → INGESTING → CONVERTING → INGESTED
	steps := []job.Status{job.StatusIngesting, job.StatusConverting, job.StatusIngested}
	for _, next := range steps {
		j, err = ti.svc.Transition(ctx, job.TransitionRequest{
			JobID:    j.ID,
			ToStatus: next,
			WorkerID: "test-worker",
			Note:     fmt.Sprintf("advancing to %s", next),
		})
		require.NoError(t, err, "transition to %s", next)
		assert.Equal(t, next, j.Status, "expected status %s", next)
	}

	// Verify DB state reflects final status
	fetched, err := ti.svc.GetJob(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusIngested, fetched.Status)

	// Illegal transition must be rejected (INGESTED → COMPLETED is not a legal hop)
	_, err = ti.svc.Transition(ctx, job.TransitionRequest{
		JobID:    j.ID,
		ToStatus: job.StatusCompleted,
		WorkerID: "test-worker",
	})
	require.Error(t, err, "illegal transition should return an error")
	assert.Contains(t, err.Error(), "illegal")

	// JSONB merge must accumulate — not replace — job_state keys
	err = ti.svc.MergeJobState(ctx, j.ID, job.StateData{"key_a": "value_a"})
	require.NoError(t, err)
	err = ti.svc.MergeJobState(ctx, j.ID, job.StateData{"key_b": "value_b"})
	require.NoError(t, err)

	fetched, err = ti.svc.GetJob(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, "value_a", fetched.JobState["key_a"], "first merge key must persist after second merge")
	assert.Equal(t, "value_b", fetched.JobState["key_b"], "second merge key must exist")
}

// ── TestSingleFilePipeline ───────────────────────────────────────────────────
//
// Full ingestion → recognition (real Claude API) → export for a single PDF.
// Verifies artifacts at each stage and asserts the final result.json structure.
//
// Runtime: ~60–90s (dominated by Claude API response time).

func TestSingleFilePipeline(t *testing.T) {
	ti := connect(t)

	// Unique key per run so concurrent dev activity doesn't interfere
	sourceKey := fmt.Sprintf("test-single/%d/G145377371_Invoice.pdf", time.Now().UnixNano())
	localFile := testFilesBase + "/ms_invoice_one/G145377371_Invoice.pdf"

	// ── Stage 1: Upload PDF to MinIO input bucket ────────────────────────────
	ti.uploadTestFile(t, localFile, testInputBucket, sourceKey)
	t.Cleanup(func() { ti.cleanupInputKey(t, sourceKey) })

	// ── Stage 2: Ingestion ───────────────────────────────────────────────────
	ingWorker := ti.buildIngestionWorker(t, "test-ingest-1")
	err := ingWorker.ProcessFile(context.Background(), ingestion.DetectedFile{
		Bucket:   testInputBucket,
		Key:      sourceKey,
		Filename: "G145377371_Invoice.pdf",
		Size:     fileSize(t, localFile),
		TenantID: testTenantID,
		SystemID: testSystemID,
	})
	require.NoError(t, err, "ingestion failed")

	jobID := ti.findJobBySourceKey(t, testInputBucket, sourceKey)
	t.Cleanup(func() { ti.cleanupJob(t, jobID) })

	j := ti.waitForStatus(t, jobID, job.StatusIngested, 60*time.Second)
	require.NotNil(t, j.PageCount, "page_count must be set after ingestion")
	assert.Greater(t, *j.PageCount, 0, "must have at least one page")

	// Verify document and page records were created
	var docCount int
	ti.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM job_documents WHERE job_id = $1`, jobID).Scan(&docCount)
	assert.Equal(t, 1, docCount, "single-file job must have exactly one document")

	var pageCount int
	ti.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM job_pages WHERE job_id = $1`, jobID).Scan(&pageCount)
	assert.Equal(t, *j.PageCount, pageCount, "page rows must match page_count")

	// ── Stage 3: Recognition (real Claude API call) ──────────────────────────
	recWorker := ti.buildRecognitionWorker(t, "test-recog-1")
	// processJob claims the next INGESTED job — ensure ours is the only one
	err = recWorker.ProcessJob(context.Background(), jobID)
	require.NoError(t, err, "recognition failed")

	j = ti.waitForStatus(t, jobID, job.StatusRecognized, 120*time.Second)
	_ = j

	// Verify fields were written
	var fieldCount int
	ti.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM job_fields WHERE job_id = $1 AND value_source = 'llm'`, jobID).Scan(&fieldCount)
	assert.Greater(t, fieldCount, 0, "recognition must write at least one LLM field")

	// ── Stage 4: Export ──────────────────────────────────────────────────────
	expWorker := ti.buildExportWorker(t, "test-export-1")
	err = expWorker.ProcessJob(context.Background(), jobID)
	require.NoError(t, err, "export failed")

	ti.waitForStatus(t, jobID, job.StatusCompleted, 30*time.Second)

	// ── Stage 5: Verify result.json in MinIO ─────────────────────────────────
	expectedKey := fmt.Sprintf("jobs/%s/%s/%s/export/result.json", testTenantID, testSystemID, jobID)
	obj, err := ti.mc.GetObject(
		context.Background(), testJobsBucket, expectedKey, minio.GetObjectOptions{},
	)
	require.NoError(t, err, "result.json must exist in MinIO at %s", expectedKey)
	defer obj.Close()

	raw, err := io.ReadAll(obj)
	require.NoError(t, err)

	var payload exportPayload
	require.NoError(t, json.Unmarshal(raw, &payload), "result.json must be valid JSON")
	assert.Equal(t, jobID, payload.JobID, "result.json job_id must match")
	assert.Equal(t, testTenantID, payload.TenantID)
	assert.NotEmpty(t, payload.Fields, "result.json must contain at least one field")
	assert.NotZero(t, payload.ExportedAt, "exported_at must be set")
}

// ── TestFolderJobPipeline ────────────────────────────────────────────────────
//
// Full pipeline for a two-file folder job with a _READY.json signal.
// Verifies multi-document structure and combined export.
//
// Runtime: ~90–150s (two Claude API calls).

func TestFolderJobPipeline(t *testing.T) {
	ti := connect(t)

	prefix := fmt.Sprintf("test-folder-%d", time.Now().UnixNano())
	key1 := prefix + "/G145377371_Invoice.pdf"
	key2 := prefix + "/G139612319_Invoice.pdf"
	keyReady := prefix + "/_READY.json"

	localFile1 := testFilesBase + "/ms_invoice_one/G145377371_Invoice.pdf"
	localFile2 := testFilesBase + "/ms_invoice_two/G139612319_Invoice.pdf"

	// ── Upload all three files to MinIO ──────────────────────────────────────
	ti.uploadTestFile(t, localFile1, testInputBucket, key1)
	ti.uploadTestFile(t, localFile2, testInputBucket, key2)
	readyJSON := `{"source":"integration-test"}`
	_, err := ti.mc.PutObject(
		context.Background(), testInputBucket, keyReady,
		strings.NewReader(readyJSON), int64(len(readyJSON)),
		minio.PutObjectOptions{ContentType: "application/json"},
	)
	require.NoError(t, err, "upload _READY.json")

	t.Cleanup(func() {
		ti.cleanupInputKey(t, key1)
		ti.cleanupInputKey(t, key2)
		ti.cleanupInputKey(t, keyReady)
	})

	// ── Build folder DetectedFile using accumulator ───────────────────────────
	fa := ingestion.NewFolderAccumulator(ti.db, ti.log)

	file1 := ingestion.DetectedFile{Bucket: testInputBucket, Key: key1, Filename: "G145377371_Invoice.pdf", Size: fileSize(t, localFile1), TenantID: testTenantID, SystemID: testSystemID}
	file2 := ingestion.DetectedFile{Bucket: testInputBucket, Key: key2, Filename: "G139612319_Invoice.pdf", Size: fileSize(t, localFile2), TenantID: testTenantID, SystemID: testSystemID}

	_, ready := fa.Add(prefix, file1)
	assert.False(t, ready, "job must not be ready after first file")
	_, ready = fa.Add(prefix, file2)
	assert.False(t, ready, "job must not be ready after second file (no signal yet)")

	folderJob, ready := fa.Signal(prefix, prefix, readyJSON)
	require.True(t, ready, "Signal must return ready=true once files are accumulated")
	assert.True(t, folderJob.IsFolder)
	assert.Len(t, folderJob.AllKeys, 2, "folder job must contain both files")

	// ── Ingestion ─────────────────────────────────────────────────────────────
	ingWorker := ti.buildIngestionWorker(t, "test-ingest-folder")
	err = ingWorker.ProcessFile(context.Background(), folderJob)
	require.NoError(t, err, "folder ingestion failed")

	jobID := ti.findJobBySourceKey(t, testInputBucket, folderJob.Key)
	t.Cleanup(func() { ti.cleanupJob(t, jobID) })

	j := ti.waitForStatus(t, jobID, job.StatusIngested, 120*time.Second)
	require.NotNil(t, j.PageCount)
	assert.Greater(t, *j.PageCount, 1, "folder job must have pages from both documents")

	// Verify two document records
	var docCount int
	ti.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM job_documents WHERE job_id = $1`, jobID).Scan(&docCount)
	assert.Equal(t, 2, docCount, "folder job must have two document records")

	// ── Recognition ──────────────────────────────────────────────────────────
	recWorker := ti.buildRecognitionWorker(t, "test-recog-folder")
	err = recWorker.ProcessJob(context.Background(), jobID)
	require.NoError(t, err, "recognition failed")
	ti.waitForStatus(t, jobID, job.StatusRecognized, 180*time.Second)

	var fieldCount int
	ti.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM job_fields WHERE job_id = $1`, jobID).Scan(&fieldCount)
	assert.Greater(t, fieldCount, 0, "must have recognized fields")

	// ── Export ────────────────────────────────────────────────────────────────
	expWorker := ti.buildExportWorker(t, "test-export-folder")
	err = expWorker.ProcessJob(context.Background(), jobID)
	require.NoError(t, err, "export failed")
	ti.waitForStatus(t, jobID, job.StatusCompleted, 30*time.Second)

	// ── Verify result.json ────────────────────────────────────────────────────
	expectedKey := fmt.Sprintf("jobs/%s/%s/%s/export/result.json", testTenantID, testSystemID, jobID)
	obj, err := ti.mc.GetObject(context.Background(), testJobsBucket, expectedKey, minio.GetObjectOptions{})
	require.NoError(t, err, "result.json must exist at %s", expectedKey)
	defer obj.Close()

	raw, err := io.ReadAll(obj)
	require.NoError(t, err)

	var payload exportPayload
	require.NoError(t, json.Unmarshal(raw, &payload))
	assert.Equal(t, jobID, payload.JobID)
	assert.NotEmpty(t, payload.Fields, "folder job result.json must have fields")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// exportPayload mirrors export.ExportPayload for JSON unmarshalling in tests.
type exportPayload struct {
	JobID      string         `json:"job_id"`
	TenantID   string         `json:"tenant_id"`
	SystemID   string         `json:"system_id"`
	Filename   string         `json:"source_filename"`
	PageCount  *int           `json:"page_count,omitempty"`
	Fields     []exportField  `json:"fields"`
	ExportedAt time.Time      `json:"exported_at"`
}

type exportField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// fileSize returns the byte size of a local file. Fails the test if unreadable.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "stat test file %s", path)
	return info.Size()
}
