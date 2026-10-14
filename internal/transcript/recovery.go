package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// repairTailLocked removes only an incomplete final JSON record before continuing.
func (s *Store) repairTailLocked(path string) error {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	c := s.cacheLocked(id)
	if c.repaired {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		c.repaired = true
		return nil
	}
	if err != nil {
		return err
	}
	end := len(data)
	if end > 0 && data[end-1] != '\n' {
		start := bytes.LastIndexByte(data, '\n') + 1
		if !json.Valid(data[start:]) {
			if err := os.Truncate(path, int64(start)); err != nil {
				return err
			}
		} else {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			_, writeErr := f.Write([]byte{'\n'})
			if err := errors.Join(writeErr, f.Close()); err != nil {
				return err
			}
		}
	}
	c.repaired = true
	return nil
}

// Position seeds a writer from the persisted leaf and inference sequence.
func (s *Store) Position(ctx context.Context, id string) (string, int, int, error) {
	if err := ValidateID(id); err != nil {
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
	return c.leafMessageID, c.inference, c.exchange, nil
}

type writerLock struct{ file *os.File }

// Claim permits one live writer per session, including across processes.
func (s *Store) Claim(id string) (func() error, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writers == nil {
		s.writers = make(map[string]*writerLock)
	}
	if s.writers[id] != nil {
		return nil, fmt.Errorf("session %s already has a writer", id)
	}
	f, err := lockFile(s.transcriptPath(id) + ".lock")
	if err != nil {
		return nil, fmt.Errorf("claim session %s: %w", id, err)
	}
	s.writers[id] = &writerLock{file: f}
	// Another process may have appended since our previous lease or Load.
	// Rebuild the leaf, counters, state, and tail-repair status from the log.
	delete(s.sessions, id)
	var once sync.Once
	var closeErr error
	return func() error {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			closeErr = f.Close()
			delete(s.writers, id)
		})
		return closeErr
	}, nil
}

// ValidateID rejects identifiers that can escape a session's storage directory.
func ValidateID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid session or attachment ID %q", id)
	}
	return nil
}
