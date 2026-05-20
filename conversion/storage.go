package conversion

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

func (s *Storage) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w", bucket, key, err)
	}
	return obj, nil
}

func (s *Storage) UploadJPEG(ctx context.Context, key, srcPath string) (int64, error) {
	info, err := s.client.FPutObject(ctx, s.jobsBucket, key, srcPath,
		minio.PutObjectOptions{ContentType: "image/jpeg"},
	)
	if err != nil {
		return 0, fmt.Errorf("upload %s: %w", key, err)
	}
	return info.Size, nil
}
