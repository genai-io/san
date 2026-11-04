package fs

import (
	"context"
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

// FileViews records, per cleaned absolute path, the file state the model
// last observed in a conversation, not file contents. Edit and Write consult it
// to enforce the contract their schemas promise: modifying a file "requires a
// current view" — the file must have been read (or written) before it is modified,
// and one that changed on disk since must be re-read. Without this, an edit against an
// outdated view surfaces as a confusing "old_string was not found" failure
// instead of naming the real cause.
type FileViews struct {
	mu    sync.Mutex
	views map[string]fileStamp
}

var defaultFileViews = NewFileViews()

type fileViewsKey struct{}

func NewFileViews() *FileViews { return &FileViews{views: make(map[string]fileStamp)} }

// WithFileViews binds observations to a conversation, without resetting other owners.
func WithFileViews(ctx context.Context, views *FileViews) context.Context {
	return context.WithValue(ctx, fileViewsKey{}, views)
}

func observations(contexts []context.Context) *FileViews {
	if len(contexts) != 0 {
		if views, ok := contexts[0].Value(fileViewsKey{}).(*FileViews); ok && views != nil {
			return views
		}
	}
	return defaultFileViews
}

func fileViewKey(filePath string) string { return filepath.Clean(filePath) }

// recordFileRead remembers the on-disk state of filePath at read time.
func recordFileRead(filePath string, info os.FileInfo, contexts ...context.Context) {
	store := observations(contexts)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.views[fileViewKey(filePath)] = fileStamp{modTime: info.ModTime(), size: info.Size()}
}

// RecordFileWritten refreshes the view right after Edit, Write or the app
// changed the file, so that change doesn't read as an external modification.
func RecordFileWritten(filePath string, contexts ...context.Context) {
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}
	recordFileRead(filePath, info, contexts...)
}

// ResetFileViews forgets every recorded observation. The app calls it when
// the conversation the model sees is replaced (/clear, loading another
// session), so "observed this session" keeps meaning the current
// conversation rather than the process lifetime.
func ResetFileViews() {
	defaultFileViews.mu.Lock()
	defer defaultFileViews.mu.Unlock()
	clear(defaultFileViews.views)
}

// Viewed reports whether the model's view of filePath is current, so showing
// it the file again would repeat what it has.
func Viewed(filePath string) bool {
	view, err := viewOf(filePath)
	return err == nil && view == viewCurrent
}

// fileView classifies how current the model's view of a file is.
type fileView int

const (
	viewNone    fileView = iota // file never read or written this session
	viewStale                   // file changed on disk after the last observation
	viewCurrent                 // on-disk state matches the last observation
)

// viewOf reports how the file's on-disk state relates to the model's last
// observation of it. Edit distinguishes stale from none: a stale view may
// still be edited when old_string matches unambiguously (the result carries
// a warning), while a never-observed file is always rejected.
func viewOf(filePath string, contexts ...context.Context) (fileView, error) {
	store := observations(contexts)
	store.mu.Lock()
	stamp, seen := store.views[fileViewKey(filePath)]
	store.mu.Unlock()
	if !seen {
		return viewNone, nil
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return viewNone, fmt.Errorf("failed to stat file: %s", err)
	}
	if !info.ModTime().Equal(stamp.modTime) || info.Size() != stamp.size {
		return viewStale, nil
	}
	return viewCurrent, nil
}

// requireObservedView rejects a file the model never observed this session
// and otherwise reports how current the observation is. Edit uses it
// directly — it tolerates viewStale with its own soft path — so the
// never-read policy and its model-facing message live here alone.
func requireObservedView(filePath string, contexts ...context.Context) (fileView, error) {
	view, err := viewOf(filePath, contexts...)
	if err != nil {
		return view, err
	}
	if view == viewNone {
		return view, fmt.Errorf("%s has not been read in this session; Read it before modifying it", filePath)
	}
	return view, nil
}

// requireCurrentView is the strict gate: the file must have been observed
// this session and be unchanged on disk since. Write uses it for overwrites
// — replacing a whole file from a stale view would silently destroy the
// unseen changes, so unlike Edit there is no soft path.
func requireCurrentView(filePath string, contexts ...context.Context) error {
	view, err := requireObservedView(filePath, contexts...)
	if err != nil {
		return err
	}
	if view == viewStale {
		return fmt.Errorf("%s has changed on disk since it was last read; Read it again and retry against its current content", filePath)
	}
	return nil
}
