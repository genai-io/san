package runtime

import (
	"fmt"

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
	cwd := projection.ExecutionCWD()
	// Runtime needs an explicit execution directory even for older recordings.
	if cwd == "" || params.CWD == "" {
		return session.Unfinished{}, fmt.Errorf("execution directory is missing")
	}
	if err := session.ValidateResumeDirectory(cwd, params.CWD); err != nil {
		return session.Unfinished{}, err
	}
	params.resuming = true
	if err := s.start(params, projection.ActiveMessages()); err != nil {
		return session.Unfinished{}, err
	}
	return projection.Unfinished(), nil
}
