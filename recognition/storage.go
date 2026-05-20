package recognition

import (
	"context"
	"fmt"
	"io"

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

// ReadAll downloads an object and returns its bytes.
func (s *Storage) ReadAll(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.jobsBucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return data, nil
}
