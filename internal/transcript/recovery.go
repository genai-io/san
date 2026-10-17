package transcript

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/genai-io/sdk-go/pkg/session"
)

// Position seeds a writer from the persisted leaf and inference sequence.
func (s *Store) Position(ctx context.Context, id string) (string, int, int, error) {
	if err := ValidateStorageID(id); err != nil {
		return "", 0, 0, err
	}
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.warmupCachesLocked(id); err != nil {
		return "", 0, 0, err
	}
	c := s.sessions[id]
	return lastID(c.messages), c.inference, c.exchange, nil
}

// Claim permits one live writer per session, including across processes.
func (s *Store) Claim(id string) (func() error, error) {
	if err := ValidateStorageID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writers == nil {
		s.writers = make(map[string]*session.Writer)
	}
	if s.writers[id] != nil {
		return nil, fmt.Errorf("session %s already has a writer", id)
	}
	w, err := s.log.Write(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("claim session %s: %w", id, err)
	}
	s.writers[id] = w
	// Another process may have appended since our previous lease or Load.
	// Rebuild the counters and state from the log.
	delete(s.sessions, id)
	var once sync.Once
	var closeErr error
	return func() error {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			closeErr = w.Close()
			delete(s.writers, id)
		})
		return closeErr
	}, nil
}

// ValidateStorageID rejects identifiers that can escape a session's storage directory.
func ValidateStorageID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid session or attachment ID %q", id)
	}
	return nil
}
