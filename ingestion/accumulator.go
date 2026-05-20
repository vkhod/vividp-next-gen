package ingestion

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const folderTTL = 5 * time.Minute

// FolderAccumulator tracks pending folder-mode batches in PostgreSQL.
// This replaces the previous in-memory map, enabling multiple ingestion
// instances to safely accumulate files for the same folder prefix.
type FolderAccumulator struct {
	db  *pgxpool.Pool
	log *slog.Logger
}

func NewFolderAccumulator(db *pgxpool.Pool, log *slog.Logger) *FolderAccumulator {
	fa := &FolderAccumulator{
		db:  db,
		log: log.With("module", "accumulator"),
	}
	go fa.expireLoop()
	return fa
}

// Add records one file as belonging to the given folder prefix.
// If a _READY signal already exists for this prefix (signal arrived first),
// returns the completed DetectedFile and true — the caller must dispatch it.
func (fa *FolderAccumulator) Add(prefix string, f DetectedFile) (DetectedFile, bool) {
	ctx := context.Background()

	// Upsert the file row (ignore duplicates — NATS may redeliver)
	_, err := fa.db.Exec(ctx, `
		INSERT INTO folder_accumulation (prefix, file_key, filename, size_bytes, tenant_id, system_id, bucket)
		VALUES ($1, $2, $3, $4, $5::uuid, $6::uuid, $7)
		ON CONFLICT (prefix, file_key) DO NOTHING
	`, prefix, f.Key, f.Filename, f.Size, f.TenantID, f.SystemID, f.Bucket)
	if err != nil {
		fa.log.Warn("accumulator Add failed", "prefix", prefix, "key", f.Key, "error", err)
		return DetectedFile{}, false
	}

	// Check whether a ready signal arrived before the files
	var readyExists bool
	var metaContent *string
	err = fa.db.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM folder_accumulation WHERE prefix = $1 AND ready = TRUE AND file_key = '_READY_SIGNAL'),
			(SELECT meta_content FROM folder_accumulation WHERE prefix = $1 AND file_key = '_READY_SIGNAL' LIMIT 1)
	`, prefix).Scan(&readyExists, &metaContent)
	if err != nil || !readyExists {
		return DetectedFile{}, false
	}

	// Signal was already there — release immediately
	meta := ""
	if metaContent != nil {
		meta = *metaContent
	}
	return fa.release(ctx, prefix, filepath.Base(prefix), meta)
}

// Signal is called when a _READY or _READY.json arrives for a folder prefix.
// If files are already accumulated, returns the completed DetectedFile and true.
// If no files yet, persists the signal so the next Add() call triggers the job.
func (fa *FolderAccumulator) Signal(prefix, folderName, metaContent string) (DetectedFile, bool) {
	ctx := context.Background()

	// Upsert the sentinel signal row (marks the prefix as ready)
	_, err := fa.db.Exec(ctx, `
		INSERT INTO folder_accumulation (prefix, file_key, filename, size_bytes, tenant_id, system_id, bucket, ready, meta_content)
		SELECT $1, '_READY_SIGNAL', '_READY_SIGNAL', 0,
			coalesce((SELECT tenant_id FROM folder_accumulation WHERE prefix = $1 AND file_key != '_READY_SIGNAL' LIMIT 1), '00000000-0000-0000-0000-000000000001'::uuid),
			coalesce((SELECT system_id FROM folder_accumulation WHERE prefix = $1 AND file_key != '_READY_SIGNAL' LIMIT 1), '00000000-0000-0000-0000-000000000002'::uuid),
			coalesce((SELECT bucket    FROM folder_accumulation WHERE prefix = $1 AND file_key != '_READY_SIGNAL' LIMIT 1), 'input'),
			TRUE, $2
		ON CONFLICT (prefix, file_key) DO UPDATE SET ready = TRUE, meta_content = EXCLUDED.meta_content
	`, prefix, metaContent)
	if err != nil {
		fa.log.Warn("accumulator Signal failed", "prefix", prefix, "error", err)
		return DetectedFile{}, false
	}

	// Count non-sentinel files
	var fileCount int
	fa.db.QueryRow(ctx, `
		SELECT count(*) FROM folder_accumulation
		WHERE prefix = $1 AND file_key != '_READY_SIGNAL'
	`, prefix).Scan(&fileCount)

	if fileCount == 0 {
		fa.log.Warn("signal received but no files accumulated yet — waiting", "prefix", prefix)
		return DetectedFile{}, false
	}

	return fa.release(ctx, prefix, folderName, metaContent)
}

// release collects all accumulated files for a prefix, deletes the rows, and returns
// a folder-mode DetectedFile. Called under no lock — all state is in PostgreSQL.
func (fa *FolderAccumulator) release(ctx context.Context, prefix, folderName, metaContent string) (DetectedFile, bool) {
	rows, err := fa.db.Query(ctx, `
		SELECT file_key, filename, size_bytes, tenant_id::text, system_id::text, bucket
		FROM folder_accumulation
		WHERE prefix = $1 AND file_key != '_READY_SIGNAL'
		ORDER BY created_at
	`, prefix)
	if err != nil {
		fa.log.Warn("accumulator release query failed", "prefix", prefix, "error", err)
		return DetectedFile{}, false
	}
	defer rows.Close()

	var files []FileEntry
	var tenantID, systemID, bucket string
	var totalSize int64
	for rows.Next() {
		var fe FileEntry
		if err := rows.Scan(&fe.Key, &fe.Filename, &fe.Size, &tenantID, &systemID, &bucket); err != nil {
			continue
		}
		files = append(files, fe)
		totalSize += fe.Size
	}
	rows.Close()

	if len(files) == 0 {
		return DetectedFile{}, false
	}

	// Delete the accumulated rows for this prefix
	fa.db.Exec(ctx, `DELETE FROM folder_accumulation WHERE prefix = $1`, prefix)

	return DetectedFile{
		Bucket:      bucket,
		Key:         prefix,
		Filename:    folderName,
		Size:        totalSize,
		TenantID:    tenantID,
		SystemID:    systemID,
		IsFolder:    true,
		AllKeys:     files,
		MetaContent: metaContent,
	}, true
}

// expireLoop periodically deletes stale accumulation rows (no signal within TTL).
func (fa *FolderAccumulator) expireLoop() {
	ticker := time.NewTicker(folderTTL / 2)
	defer ticker.Stop()
	for range ticker.C {
		ctx := context.Background()
		result, err := fa.db.Exec(ctx, `
			DELETE FROM folder_accumulation
			WHERE ready = FALSE AND created_at < now() - $1::interval
		`, folderTTL.String())
		if err != nil {
			fa.log.Warn("accumulator expire failed", "error", err)
			continue
		}
		if n := result.RowsAffected(); n > 0 {
			fa.log.Warn("expired accumulated folder files (no signal received)", "rows_deleted", n)
		}
	}
}
