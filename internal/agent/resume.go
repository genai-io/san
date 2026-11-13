package agent

import (
	"fmt"
	"path/filepath"

	"github.com/genai-io/san/internal/session"
)

// Resume rebuilds an idle runtime from its opened typed owner. The host supplies
// current settings and trust; interrupted work is returned, never replayed.
func (s *Runtime) Resume(params BuildParams) (session.Unfinished, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil {
		return session.Unfinished{}, fmt.Errorf("agent session already active")
	}
	if params.Recording == nil {
		return session.Unfinished{}, fmt.Errorf("resume requires an opened typed session")
	}
	if err := params.prepareRecording(); err != nil {
		return session.Unfinished{}, err
	}
	projection := params.Recording.Snapshot()
	cwd := projection.Identity().InitialCWD
	if saved := projection.State().ExecutionCWD; saved != nil {
		cwd = *saved
	}
	if err := validateExecutionDirectory(cwd, params.CWD); err != nil {
		return session.Unfinished{}, err
	}
	params.resuming = true
	if err := s.start(params, projection.ActiveMessages()); err != nil {
		return session.Unfinished{}, err
	}
	return projection.Unfinished(), nil
}

func validateExecutionDirectory(saved, requested string) error {
	canonical := func(path string) (string, error) {
		if path == "" {
			return "", fmt.Errorf("execution directory is missing")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(absolute)
	}
	persisted, err := canonical(saved)
	if err != nil {
		return fmt.Errorf("cannot validate saved session directory %q: %w", saved, err)
	}
	current, err := canonical(requested)
	if err != nil {
		return fmt.Errorf("cannot validate requested session directory %q: %w", requested, err)
	}
	if persisted != current {
		return fmt.Errorf("session directory mismatch: saved directory %q differs from requested directory %q; reopen San in the saved directory to resume", saved, requested)
	}
	return nil
}
