package ingestion

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"

	"vividp/job"
	"github.com/minio/minio-go/v7"
)

// Reconciler scans the input bucket at startup and queues any files that have
// no corresponding job — recovering from service downtime or missed webhook events.
type Reconciler struct {
	svc     *job.Service
	storage *Storage
	cfg     Config
	acc     *FolderAccumulator
	log     *slog.Logger
}

func NewReconciler(svc *job.Service, storage *Storage, cfg Config, acc *FolderAccumulator, log *slog.Logger) *Reconciler {
	return &Reconciler{svc: svc, storage: storage, cfg: cfg, acc: acc, log: log.With("module", "reconciler")}
}

// Run scans the input bucket and queues unprocessed files.
// Returns the number of items queued.
//
// Performance note: scan cost grows linearly with input bucket size.
// After months of operation with clients who leave processed files in the bucket,
// this startup scan can become slow. Two mitigation options:
//
//  1. Delete (or move to archive/) input files when a job reaches EXPORTED status.
//     Clean, event-driven, but requires the export station to write to the input bucket.
//  2. A periodic maintenance cron that sweeps processed keys out of the input bucket.
//     More decoupled, but adds operational overhead.
//
// TODO: implement one of the above before going to production at scale.
func (r *Reconciler) Run(ctx context.Context, workCh chan<- DetectedFile) int {
	r.log.Info("scanning input bucket for unprocessed files", "bucket", r.cfg.InputBucket)

	objects, err := r.storage.ListObjects(ctx, r.cfg.InputBucket, "")
	if err != nil {
		r.log.Warn("could not list input bucket", "bucket", r.cfg.InputBucket, "error", err)
		return 0
	}

	// ── Pass 1: sort objects into singles and folder groups ───────────────────
	type folderEntry struct {
		files       []DetectedFile
		signalKey   string
		metaContent string
	}
	folders := map[string]*folderEntry{}

	type singleCandidate struct {
		obj      minio.ObjectInfo
		tenantID string
		systemID string
	}
	var singles []singleCandidate

	for _, obj := range objects {
		key := obj.Key
		prefix := folderPrefix(key)

		if isSignalFile(key) {
			if prefix == "" {
				continue // root-level signal with no folder context — skip
			}
			fe := folders[prefix]
			if fe == nil {
				fe = &folderEntry{}
				folders[prefix] = fe
			}
			fe.signalKey = key
			if filepath.Base(key) == "_READY.json" {
				if rc, err := r.storage.ReadObject(ctx, r.cfg.InputBucket, key); err == nil {
					if raw, _ := io.ReadAll(io.LimitReader(rc, 1<<16)); len(raw) > 0 {
						fe.metaContent = string(raw)
					}
					rc.Close()
				}
			}
			continue
		}

		if shouldIgnore(key) {
			continue
		}

		tenantID, systemID := extractRouting(key, r.cfg.DefaultTenantID, r.cfg.DefaultSystemID)

		if prefix != "" {
			fe := folders[prefix]
			if fe == nil {
				fe = &folderEntry{}
				folders[prefix] = fe
			}
			fe.files = append(fe.files, DetectedFile{
				Bucket:   r.cfg.InputBucket,
				Key:      key,
				Filename: filepath.Base(key),
				Size:     obj.Size,
				TenantID: tenantID,
				SystemID: systemID,
			})
		} else {
			singles = append(singles, singleCandidate{obj, tenantID, systemID})
		}
	}

	// ── Pass 2: bulk DB lookup — one round-trip for all candidates ────────────
	var allKeys []string
	for _, s := range singles {
		allKeys = append(allKeys, s.obj.Key)
	}
	for prefix, fe := range folders {
		if fe.signalKey != "" && len(fe.files) > 0 {
			allKeys = append(allKeys, prefix) // folder job key = prefix
		}
	}

	existing, err := r.svc.KeysWithJobs(ctx, r.cfg.InputBucket, allKeys)
	if err != nil {
		r.log.Warn("DB bulk check failed", "error", err)
		return 0
	}

	// ── Pass 3: queue anything not already in DB ──────────────────────────────
	queued := 0

	for _, s := range singles {
		if existing[s.obj.Key] {
			continue
		}
		select {
		case workCh <- DetectedFile{
			Bucket:   r.cfg.InputBucket,
			Key:      s.obj.Key,
			Filename: filepath.Base(s.obj.Key),
			Size:     s.obj.Size,
			TenantID: s.tenantID,
			SystemID: s.systemID,
		}:
			r.log.Info("queued missed file", "file", filepath.Base(s.obj.Key))
			queued++
		default:
			r.log.Warn("work queue full — skipping file", "key", s.obj.Key)
		}
	}

	for prefix, fe := range folders {
		if fe.signalKey == "" || len(fe.files) == 0 {
			if len(fe.files) > 0 {
				r.log.Warn("folder has files but no signal — skipping", "prefix", prefix, "file_count", len(fe.files))
			}
			continue
		}
		if existing[prefix] {
			continue
		}

		var totalSize int64
		var allFileKeys []FileEntry
		tenantID, systemID := fe.files[0].TenantID, fe.files[0].SystemID
		for _, f := range fe.files {
			totalSize += f.Size
			allFileKeys = append(allFileKeys, FileEntry{Key: f.Key, Filename: f.Filename, Size: f.Size})
		}

		select {
		case workCh <- DetectedFile{
			Bucket:      r.cfg.InputBucket,
			Key:         prefix,
			Filename:    filepath.Base(prefix),
			Size:        totalSize,
			TenantID:    tenantID,
			SystemID:    systemID,
			IsFolder:    true,
			AllKeys:     allFileKeys,
			MetaContent: fe.metaContent,
		}:
			r.log.Info("queued missed folder", "folder", filepath.Base(prefix), "file_count", len(allFileKeys))
			queued++
		default:
			r.log.Warn("work queue full — skipping folder", "prefix", prefix)
		}
	}

	r.log.Info("reconciliation complete", "queued", queued)
	return queued
}
