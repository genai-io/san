package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/transcript"
)

const (
	trashDirName   = "trash"
	deletedFile    = "deleted.json"
	purgingPrefix  = ".purging-"
	trashRetention = 7 * 24 * time.Hour
)

var (
	// ErrPinned means a pinned session was asked to leave the live set.
	ErrPinned = errors.New("session: pinned; unpin it first")
	// ErrExists means restoring would overwrite a live session.
	ErrExists = errors.New("session: a live session already has this ID")
)

// library is a project's collection of sessions. Each session is a directory,
// so leaving the live set, coming back and being removed are renames of that
// directory: a crash leaves a session live or trashed, never split.
type library struct{ root string }

func (l library) sessionDir(id string) string { return filepath.Join(l.root, sessionsDirName, id) }
func (l library) trashRoot() string           { return filepath.Join(l.root, trashDirName) }
func (l library) trashDir(id string) string   { return filepath.Join(l.trashRoot(), id) }
func (l library) cacheFile(id string) string  { return filepath.Join(l.root, "cache", id+".json") }

type deletedRecord struct {
	DeletedAt int64 `json:"deleted_at"` // Unix ms
}

// Trashed is a session waiting in the trash. DeletedAt is zero when its marker
// is missing or damaged; such a session is never purged automatically.
type Trashed struct {
	ID        string
	DeletedAt time.Time
}

// Deadline is when the session becomes eligible for purge, or zero if unknown.
func (t Trashed) Deadline() time.Time {
	if t.DeletedAt.IsZero() {
		return time.Time{}
	}
	return t.DeletedAt.Add(trashRetention)
}

// trash moves a live session to the trash. It needs the writer claim, so a
// session open anywhere (ErrBusy) cannot be trashed, and it refuses a pinned one.
// The claim is held across the rename, so nothing can open the session midway.
func (l library) trash(ctx context.Context, id string, now time.Time) error {
	if err := transcript.ValidateStorageID(id); err != nil {
		return err
	}
	dir := l.sessionDir(id)
	if _, err := os.Stat(filepath.Join(dir, eventsFileName)); err != nil {
		return err
	}
	log, err := openEventLog(ctx, dir)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = log.close()
		}
	}()
	attrs, err := readAttrs(dir)
	if err != nil {
		return err
	}
	if attrs.Pinned {
		return ErrPinned
	}
	target := l.trashDir(id)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("session %s is already in the trash: %w", id, ErrExists)
	}
	data, err := json.Marshal(deletedRecord{DeletedAt: now.UnixMilli()})
	if err != nil {
		return err
	}
	marker := filepath.Join(dir, deletedFile)
	if err := atomicfile.Write(marker, data, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(l.trashRoot(), 0o700); err != nil {
		_ = os.Remove(marker)
		return err
	}
	if err := os.Rename(dir, target); err != nil {
		_ = os.Remove(marker)
		return err
	}
	closed = true
	err = log.close()
	// The writer lock file moved with the directory; its lease is released.
	_ = os.Remove(filepath.Join(target, eventsFileName+".lock"))
	_ = os.Remove(l.cacheFile(id))
	return err
}

// restore moves a trashed session back, never overwriting a live one.
func (l library) restore(ctx context.Context, id string) error {
	if err := transcript.ValidateStorageID(id); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	src, dst := l.trashDir(id), l.sessionDir(id)
	if _, err := os.Stat(filepath.Join(src, eventsFileName)); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrExists, err)
	}
	_ = os.Remove(filepath.Join(dst, deletedFile))
	return nil
}

// trashed lists the trash, oldest first. Names starting with "." are staging.
func (l library) trashed(ctx context.Context) ([]Trashed, error) {
	entries, err := os.ReadDir(l.trashRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Trashed
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		t := Trashed{ID: e.Name()}
		var rec deletedRecord
		if data, err := os.ReadFile(filepath.Join(l.trashDir(e.Name()), deletedFile)); err == nil && strictEventJSON(data, &rec) == nil && rec.DeletedAt > 0 {
			t.DeletedAt = time.UnixMilli(rec.DeletedAt)
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DeletedAt.Equal(out[j].DeletedAt) {
			return out[i].DeletedAt.Before(out[j].DeletedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// purge permanently removes trashed sessions past their deadline and returns
// their IDs; with dryRun it only reports. A session of unknown age is kept. A
// removal is a rename to a hidden name followed by deletion, so an interrupted
// purge is finished by the next run and never leaves a half-readable session.
func (l library) purge(ctx context.Context, now time.Time, dryRun bool) ([]string, error) {
	if !dryRun {
		if err := l.finishPurges(ctx); err != nil {
			return nil, err
		}
	}
	list, err := l.trashed(ctx)
	if err != nil {
		return nil, err
	}
	var purged []string
	for _, t := range list {
		if d := t.Deadline(); d.IsZero() || now.Before(d) {
			continue
		}
		if !dryRun {
			hidden := filepath.Join(l.trashRoot(), purgingPrefix+t.ID)
			if err := os.Rename(l.trashDir(t.ID), hidden); err != nil {
				return purged, err
			}
			if err := os.RemoveAll(hidden); err != nil {
				return purged, err
			}
		}
		purged = append(purged, t.ID)
	}
	return purged, nil
}

func (l library) finishPurges(ctx context.Context) error {
	entries, err := os.ReadDir(l.trashRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), purgingPrefix) {
			if err := os.RemoveAll(filepath.Join(l.trashRoot(), e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
