package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileStamp is the on-disk state (mtime + size) a file had the last time the
// model observed it through Read, Edit, or Write.
type fileStamp struct {
	modTime time.Time
	size    int64
}

// readStamps records, per cleaned absolute path, the file state last observed
// by the model. Edit and Write consult it to enforce the contract their
// schemas promise: a file must have been read before it is modified, and a
// file that changed on disk after that read must be re-read first. Without
// this, an edit against a stale view of the file surfaces as a confusing
// "oldText was not found" failure instead of naming the real cause.
var (
	readStampsMu sync.Mutex
	readStamps   = map[string]fileStamp{}
)

func stampKey(filePath string) string { return filepath.Clean(filePath) }

// recordFileRead remembers the on-disk state of filePath at read time.
func recordFileRead(filePath string, info os.FileInfo) {
	readStampsMu.Lock()
	defer readStampsMu.Unlock()
	readStamps[stampKey(filePath)] = fileStamp{modTime: info.ModTime(), size: info.Size()}
}

// recordFileWritten refreshes the stamp right after Edit or Write changed the
// file, so the tool's own change doesn't read as an external modification.
func recordFileWritten(filePath string) {
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}
	recordFileRead(filePath, info)
}

// ResetReadStamps forgets every recorded read. The app calls it when the
// conversation the model sees is replaced (/clear, loading another session),
// so "read in this session" keeps meaning the current conversation rather
// than the process lifetime.
func ResetReadStamps() {
	readStampsMu.Lock()
	defer readStampsMu.Unlock()
	clear(readStamps)
}

// requireFreshRead enforces read-before-modify: filePath must have been read
// in this session, and its on-disk state must still match that read.
func requireFreshRead(filePath string) error {
	readStampsMu.Lock()
	stamp, seen := readStamps[stampKey(filePath)]
	readStampsMu.Unlock()
	if !seen {
		return fmt.Errorf("%s has not been read in this session; Read it before modifying it", filePath)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %s", err)
	}
	if !info.ModTime().Equal(stamp.modTime) || info.Size() != stamp.size {
		return fmt.Errorf("%s has changed on disk since it was last read; Read it again and retry against its current content", filePath)
	}
	return nil
}
