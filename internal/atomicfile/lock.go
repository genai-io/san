package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// ErrLocked reports that another open handle, in this or another process,
// already holds the lock TryLock asked for.
var ErrLocked = errors.New("lock is held")

// TryLock takes an exclusive lock on path without waiting and returns its
// release. The operating system drops the lock when the holder exits, so a
// crash never leaves a stale owner behind. The lock file itself is kept:
// removing it would let a waiter lock an unlinked inode while a newcomer
// locks a fresh one.
func TryLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlockFile(f)
			_ = f.Close()
		})
	}, nil
}
