package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"
)

// ErrClosed means the store or writer handle has been closed.
var ErrClosed = errors.New("session is closed")

// Session owns one typed writer until Close. Its snapshots contain committed
// facts only; it never runs an agent or restores permission grants.
type Session struct {
	mu     sync.Mutex // serializes preparation and commit, including source reads
	store  *Store
	writer *eventSession
}

func (s *Session) ID() string   { return s.writer.ID() }
func (s *Session) Path() string { return s.writer.Path() }
func (s *Session) Err() error   { return s.writer.Err() }

// OwnedBy compares the original store handles, not just project or session IDs.
func (s *Session) OwnedBy(store *Store) bool {
	return s != nil && s.store == store
}

// Snapshot returns the last committed prefix, including after Close or a write
// failure. Projection is immutable and its accessors detach their results.
func (s *Session) Snapshot() Projection {
	s.writer.mu.Lock()
	defer s.writer.mu.Unlock()
	return *s.writer.state.projection
}

// Append commits one atomic batch. Zero seq, version and time are filled from
// the committed tail, version 1 and the current time; an omitted type follows
// the payload. Explicit envelope values are validated, never overwritten.
func (s *Session) Append(ctx context.Context, events ...Event) error {
	return s.write(ctx, func(*Projection) ([]Event, error) {
		if len(events) == 0 {
			return nil, fmt.Errorf("empty event batch")
		}
		return events, nil
	})
}

// write keeps snapshot-dependent preparation and its commit in one critical
// section. A nil batch is a no-op, used by unchanged catalogues and blob writes.
func (s *Session) write(ctx context.Context, build func(*Projection) ([]Event, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (library{root: s.store.projectDir}).withLock(ctx, s.ID(), func() error {
		if err := s.writer.Err(); err != nil {
			return err
		}
		if err := s.store.checkID(ctx, s.ID()); err != nil {
			return err
		}
		snapshot := s.Snapshot()
		events, err := build(&snapshot)
		if err != nil || len(events) == 0 {
			return err
		}
		return s.appendLocked(ctx, events)
	})
}

func (s *Session) appendLocked(ctx context.Context, events []Event) error {
	if err := s.writer.Err(); err != nil {
		return err
	}
	if err := s.store.checkID(ctx, s.ID()); err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("empty event batch")
	}
	snapshot := s.Snapshot()
	events = slices.Clone(events)
	now := time.Now().UnixMilli()
	for i := range events {
		e := &events[i]
		if e.Payload == nil || (reflect.ValueOf(e.Payload).Kind() == reflect.Pointer && reflect.ValueOf(e.Payload).IsNil()) {
			return fmt.Errorf("nil event payload")
		}
		if e.Type == "" {
			e.Type = e.Payload.eventType()
		}
		if e.Type == EventSessionCreate {
			return fmt.Errorf("session.create is reserved for Create and Fork")
		}
		if e.Seq == 0 {
			e.Seq = uint64(len(snapshot.data.Events) + i + 1)
		}
		if e.SchemaVersion == 0 {
			e.SchemaVersion = 1
		}
		if e.Time == 0 {
			e.Time = now
		}
	}
	// Detach native payloads before validating references or reading blobs.
	raw, err := json.Marshal(events)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &events); err != nil {
		return err
	}
	validation, err := s.validation(ctx, events)
	if err != nil {
		return err
	}
	prepared, err := s.writer.prepareEventBatch(blobDigest(raw), events, validation)
	if err != nil {
		return err
	}
	_, err = s.writer.commitEventBatch(ctx, prepared)
	return err
}

// PutRequest persists an exact standalone request before an inference references
// it. The session owns the blob and cleanup cannot race its write.
func (s *Session) PutRequest(ctx context.Context, request StandaloneRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writer.Err(); err != nil {
		return "", err
	}
	if err := s.store.check(ctx); err != nil {
		return "", err
	}
	var digest string
	err := (library{root: s.store.projectDir}).withLock(ctx, s.ID(), func() error {
		var err error
		digest, err = s.requests().PutRequest(request)
		return err
	})
	return digest, err
}

// Request reads and verifies the exact standalone request identified by digest.
func (s *Session) Request(ctx context.Context, digest string) (StandaloneRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writer.Err(); err != nil {
		return StandaloneRequest{}, err
	}
	if err := s.store.check(ctx); err != nil {
		return StandaloneRequest{}, err
	}
	return s.requests().Request(digest)
}

func (s *Session) requests() blobStore {
	return blobStore{dir: filepath.Join(s.store.sessionDir(s.ID()), "blobs")}
}

// validation records acceptance facts so foreign sessions may disappear later.
// A standalone request must already be durable before its start can commit.
func (s *Session) validation(ctx context.Context, events []Event) (EventValidationContext, error) {
	out := EventValidationContext{ForeignEvents: map[string]ForeignEventEvidence{}}
	foreign := map[string]*Projection{}
	for _, e := range events {
		if start, ok := e.Payload.(*InferenceStartPayload); ok && start.Input.Standalone != "" {
			if _, err := s.requests().Request(start.Input.Standalone); err != nil {
				return out, fmt.Errorf("standalone request: %w", err)
			}
		}
		ref := e.Source
		if ref == nil || ref.Session == "" || ref.Session == s.ID() {
			continue
		}
		if err := s.store.checkID(ctx, ref.Session); err != nil {
			return out, err
		}
		key := foreignKey(ref.Session, ref.Seq)
		if fact, ok := s.writer.state.validation.ForeignEvents[key]; ok {
			out.ForeignEvents[key] = fact
			continue
		}
		p := foreign[ref.Session]
		if p == nil {
			state, err := s.store.readEvents(ctx, ref.Session, nil)
			if err != nil {
				return out, err
			}
			p = state.projection
			foreign[ref.Session] = p
		}
		source := p.event(ref.Seq)
		if source == nil {
			return out, fmt.Errorf("unresolved foreign source %s", key)
		}
		fact := ForeignEventEvidence{Type: source.Type}
		if source.Type == EventMessageAppend {
			fact.MessageIDs = p.messageIDsOf(source)
		}
		out.ForeignEvents[key] = fact
	}
	return out, nil
}

// Close drains in-flight appends, then releases the writer exactly once.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writer.Close()
}
