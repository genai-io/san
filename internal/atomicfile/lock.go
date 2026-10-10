package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WithLock serializes a read-modify-write across processes. The lock file must
// have a stable name separate from the data file, which Write replaces.
func WithLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlockFile(f)
	return fn()
}
