package export

import (
	"bytes"
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Storage struct {
	client     *minio.Client
	jobsBucket string
}

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

// UploadJSON writes JSON bytes to the jobs bucket at the given key.
func (s *Storage) UploadJSON(ctx context.Context, key string, data []byte) (int64, error) {
	info, err := s.client.PutObject(ctx, s.jobsBucket, key,
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/json"},
	)
	if err != nil {
		return 0, fmt.Errorf("upload %s: %w", key, err)
	}
	return info.Size, nil
}

// UploadToTenantBucket writes data to a tenant-configured external bucket.
// storageConfig is the tenant's storage_config JSONB — for now we write to
// the jobs bucket under an export/ prefix. Real tenant S3 support comes later.
func (s *Storage) UploadToTenantBucket(ctx context.Context, key string, data []byte, _ map[string]any) (int64, error) {
	// Phase 1: export to jobs bucket. Phase 2: use storageConfig for tenant S3.
	return s.UploadJSON(ctx, key, data)
}
