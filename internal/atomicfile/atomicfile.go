// Package atomicfile replaces a file's contents in one observable step.
//
// The content is written to a temporary file in the destination directory and
// renamed over the target, so a concurrent reader sees either the old contents
// or the new ones — never a half-written file — and a failed write leaves the
// previous version intact.
//
// The temporary name is unique per call. A fixed "<path>.tmp" would have two
// concurrent writers of the same path share one scratch file and interleave
// their bytes into it, which is exactly the corruption the rename is meant to
// prevent.
package atomicfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DirPerm is the mode used for directories created on the way to the target.
const DirPerm os.FileMode = 0o755

// Write replaces the file at path with data, creating parent directories as
// needed. The temporary file is removed on every failure path, so a failed
// write leaves nothing behind.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	// Armed until the rename succeeds, so every failure below — including a
	// panic — leaves nothing behind. Both calls are no-ops once they have run.
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	// CreateTemp always makes the file 0600; widen or narrow it to the caller's
	// mode before it becomes visible under the target name.
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// WriteJSON marshals v as indented JSON and writes it through Write.
func WriteJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return Write(path, data, perm)
}
