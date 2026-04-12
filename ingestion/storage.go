// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/storage.go
// MinIO / S3 client wrapper — identical API on-prem and cloud.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Storage wraps the MinIO client with FormStorm-specific operations.
type Storage struct {
	client     *minio.Client
	jobsBucket string // destination for processed artifacts
}

// NewStorage creates a MinIO client from environment configuration.
func NewStorage(cfg Config) (*Storage, error) {
	client, err := minio.New(cfg.StorageEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: cfg.StorageSecure,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &Storage{client: client, jobsBucket: cfg.JobsBucket}, nil
}

// DownloadToTemp downloads a file from the given bucket/key to a local path.
func (s *Storage) DownloadToTemp(ctx context.Context, bucket, key, destPath string) error {
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("get object %s/%s: %w", bucket, key, err)
	}
	defer obj.Close()

	// Use FGetObject for efficient streaming to disk
	return s.client.FGetObject(ctx, bucket, key, destPath, minio.GetObjectOptions{})
}

// UploadTIF streams a TIF page to the jobs bucket.
// Key format: jobs/{tenant}/{system}/{job-id}/pages/{NNN}/original.tif
func (s *Storage) UploadTIF(ctx context.Context, jobID, tenantID, systemID string, pageNum int, srcPath string) (string, int64, error) {
	key := fmt.Sprintf("jobs/%s/%s/%s/pages/%03d/original.tif", tenantID, systemID, jobID, pageNum)

	info, err := s.client.FPutObject(ctx, s.jobsBucket, key, srcPath,
		minio.PutObjectOptions{ContentType: "image/tiff"},
	)
	if err != nil {
		return "", 0, fmt.Errorf("upload TIF page %d: %w", pageNum, err)
	}
	return key, info.Size, nil
}

// UploadOriginal archives the source file under the job prefix.
// Key format: jobs/{tenant}/{system}/{job-id}/original.{ext}
func (s *Storage) UploadOriginal(ctx context.Context, jobID, tenantID, systemID, srcPath string) (string, int64, error) {
	ext := strings.ToLower(filepath.Ext(srcPath))
	key := fmt.Sprintf("jobs/%s/%s/%s/original%s", tenantID, systemID, jobID, ext)

	info, err := s.client.FPutObject(ctx, s.jobsBucket, key, srcPath, minio.PutObjectOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("upload original: %w", err)
	}
	return key, info.Size, nil
}

// StatObject returns size and etag for an object.
func (s *Storage) StatObject(ctx context.Context, bucket, key string) (minio.ObjectInfo, error) {
	return s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
}

// ReadObject returns a reader for an object — used for streaming content.
func (s *Storage) ReadObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
}

// ListObjects returns all objects under the given prefix in a bucket.
// An empty prefix lists the entire bucket.
//
// TODO: The reconciler's startup cost grows linearly with the input bucket size.
// As clients accumulate months of processed files, this scan will get slow.
// Two options to address this:
//  1. Delete (or move to archive/) input files when a job reaches the EXPORTED stage.
//  2. A periodic maintenance cron that sweeps processed keys out of the input bucket.
//
// Option 1 is cleaner (event-driven, no extra service), but requires the export station
// to have write access to the input bucket. Option 2 is more decoupled but adds ops overhead.
func (s *Storage) ListObjects(ctx context.Context, bucket, prefix string) ([]minio.ObjectInfo, error) {
	var objects []minio.ObjectInfo
	for obj := range s.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		objects = append(objects, obj)
	}
	return objects, nil
}
