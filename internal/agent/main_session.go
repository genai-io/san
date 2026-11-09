package agent

import (
	"fmt"

	"github.com/genai-io/san/internal/session"
)

// MainSession pairs an opened native session with its full tool-result store.
// The host owns their lifetime; validate the pair before wiring a runtime.
// Authorization and extension callbacks belong to the host's composition, not
// to this data owner.
type MainSession struct {
	Session *session.LegacySession
	Store   *session.LegacyStore
}

// Validate rejects unavailable recording and cross-project result storage.
// It does not bind callbacks or mutate services. The host must stop the runtime
// before closing or replacing its session.
func (s MainSession) Validate() error {
	if s.Session == nil || s.Store == nil {
		return fmt.Errorf("main-session runtime requires an opened session and store")
	}
	if err := s.Session.Err(); err != nil {
		return err
	}
	if s.Store.TranscriptPath(s.Session.ID()) != s.Session.TranscriptPath() {
		return fmt.Errorf("main-session recorder and result store belong to different projects")
	}
	return nil
}
