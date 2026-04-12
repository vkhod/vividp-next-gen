package ingestion

import (
	"log/slog"
	"sync"
	"time"
)

const folderTTL = 5 * time.Minute

type pendingFolder struct {
	files    []FileEntry
	tenantID string
	systemID string
	lastSeen time.Time
}

// FolderAccumulator collects individual file events for a folder until a
// _READY signal arrives, then releases them as a single folder-mode DetectedFile.
type FolderAccumulator struct {
	mu      sync.Mutex
	folders map[string]*pendingFolder
	log     *slog.Logger
}

func NewFolderAccumulator(log *slog.Logger) *FolderAccumulator {
	fa := &FolderAccumulator{
		folders: make(map[string]*pendingFolder),
		log:     log.With("module", "accumulator"),
	}
	go fa.expireLoop()
	return fa
}

// Add records a file as belonging to the given folder prefix.
func (fa *FolderAccumulator) Add(prefix string, f DetectedFile) {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	pf, ok := fa.folders[prefix]
	if !ok {
		pf = &pendingFolder{tenantID: f.TenantID, systemID: f.SystemID}
		fa.folders[prefix] = pf
	}
	pf.files = append(pf.files, FileEntry{Key: f.Key, Filename: f.Filename, Size: f.Size})
	pf.lastSeen = time.Now()
}

// Signal is called when a _READY or _READY.json file arrives for a folder.
// Returns a folder-mode DetectedFile and true if files were waiting.
// Returns false if no files were accumulated (signal arrived before any files).
func (fa *FolderAccumulator) Signal(prefix, folderName, metaContent string) (DetectedFile, bool) {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	pf, ok := fa.folders[prefix]
	if !ok || len(pf.files) == 0 {
		fa.log.Warn("signal received but no files accumulated — discarding", "prefix", prefix)
		delete(fa.folders, prefix)
		return DetectedFile{}, false
	}

	delete(fa.folders, prefix)

	var totalSize int64
	for _, fe := range pf.files {
		totalSize += fe.Size
	}

	return DetectedFile{
		Bucket:      pf.files[0].Key[:0], // placeholder — bucket set by caller
		Key:         prefix,
		Filename:    folderName,
		Size:        totalSize,
		TenantID:    pf.tenantID,
		SystemID:    pf.systemID,
		IsFolder:    true,
		AllKeys:     pf.files,
		MetaContent: metaContent,
	}, true
}

// expireLoop discards folders that have not received a signal within folderTTL.
func (fa *FolderAccumulator) expireLoop() {
	ticker := time.NewTicker(folderTTL / 2)
	defer ticker.Stop()
	for range ticker.C {
		fa.mu.Lock()
		now := time.Now()
		for prefix, pf := range fa.folders {
			if now.Sub(pf.lastSeen) > folderTTL {
				fa.log.Warn("folder expired without signal — discarding",
					"prefix", prefix, "file_count", len(pf.files))
				delete(fa.folders, prefix)
			}
		}
		fa.mu.Unlock()
	}
}
