package session

import (
	"fmt"
	"path/filepath"
)

// ValidateResumeDirectory rejects implicit execution-directory changes; it
// neither grants trust nor changes the current process working directory.
func ValidateResumeDirectory(saved, requested string) error {
	if saved == "" {
		return nil
	}
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(absolute)
	}
	persisted, err := canonical(saved)
	if err != nil {
		return fmt.Errorf("cannot validate saved session directory %q: %w; restore that directory before resuming", saved, err)
	}
	current, err := canonical(requested)
	if err != nil {
		return fmt.Errorf("cannot validate requested session directory %q: %w", requested, err)
	}
	if persisted != current {
		return fmt.Errorf("session directory mismatch: saved directory %q differs from requested directory %q; reopen San in the saved directory to resume (implicit directory switching is not supported)", saved, requested)
	}
	return nil
}
