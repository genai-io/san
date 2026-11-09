package session

// Private typed-event persistence under sessions/<sid>/. Ordinary Store.Create/Open remain schema 4.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/genai-io/san/internal/transcript"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

type eventBatchPayload struct {
	Version    int                    `json:"version"`
	Events     []Event                `json:"events"`
	Validation EventValidationContext `json:"validation"`
}
type eventState struct {
	projection *Projection
	tail       sdksession.Tail
	validation EventValidationContext
	batches    map[string]sdksession.Tail // original Expected, not duplicated history
}
type preparedEventBatch struct {
	owner      *eventSession
	batch      sdksession.Batch
	projection *Projection
	validation EventValidationContext
}

// eventSession is a typed session opened for writing: it owns the directory's
// writer and the projection of everything committed so far.
type eventSession struct {
	mu     sync.Mutex
	id     string
	path   string // events file
	log    *eventLog
	state  *eventState
	err    error // a failed write leaves the writer unfit to commit more
	closed bool
}

func (s *eventSession) ID() string   { return s.id }
func (s *eventSession) Path() string { return s.path }

// Err reports a persistence failure, or ErrClosed after Close.
func (s *eventSession) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return ErrClosed
	}
	return nil
}

// Close releases the writer after any in-flight commit.
func (s *eventSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.log != nil {
		return s.log.close()
	}
	return nil
}

func decodeEventBatchPayload(data []byte) (eventBatchPayload, error) {
	var p eventBatchPayload
	if err := checkEventJSONKeys(data); err != nil {
		return p, err
	}
	if err := requiredEventFields(data, reflect.TypeFor[eventBatchPayload]()); err != nil {
		return p, err
	}
	if err := strictEventJSON(data, &p); err != nil {
		return p, err
	}
	if p.Version != 1 || len(p.Events) == 0 {
		return p, fmt.Errorf("unsupported or empty san.events payload")
	}
	return p, nil
}

func (s *Store) createSession(ctx context.Context, batchID, sessionID string, create Event, validation EventValidationContext) (*eventSession, error) {
	// Detach and validate before acquiring a writer (which may create an empty file).
	raw, err := json.Marshal(eventBatchPayload{1, []Event{create}, validation})
	if err != nil {
		return nil, err
	}
	payload, err := decodeEventBatchPayload(raw)
	if err != nil {
		return nil, err
	}
	create = payload.Events[0]
	identity, ok := create.Payload.(*SessionCreatePayload)
	if !ok || create.Type != EventSessionCreate {
		return nil, fmt.Errorf("initial event must be session.create")
	}
	if identity.ProjectID != s.projectID {
		return nil, fmt.Errorf("event project identity mismatch")
	}
	if identity.Workspace == "" {
		identity.Workspace = s.workspace
	}
	if s.project != nil {
		workspace, err := validateProjectWorkspace(ctx, *s.project, identity.Workspace)
		if err != nil {
			return nil, err
		}
		identity.Workspace = workspace
	} else if identity.Workspace != s.workspace {
		return nil, fmt.Errorf("event workspace identity mismatch")
	}
	if err := transcript.ValidateStorageID(sessionID); err != nil {
		return nil, err
	}
	opened := &eventSession{id: sessionID, path: s.eventsPath(sessionID), state: newEventState()}
	prepared, err := opened.prepareEventBatch(batchID, []Event{create}, payload.Validation)
	if err != nil {
		return nil, err
	}
	final := s.sessionDir(sessionID)
	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("session %s already exists", sessionID)
	}
	// Build the session in a staging directory and publish it by one rename, so a
	// crash leaves either nothing discoverable or a complete session.
	if err := os.MkdirAll(s.sessionsRoot(), 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(s.sessionsRoot(), "."+sessionID+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage) // a no-op once renamed
	if opened.log, err = openEventLog(ctx, stage); err != nil {
		return nil, err
	}
	receipt, err := opened.commitEventBatch(ctx, prepared)
	if err = errors.Join(err, opened.log.close()); err != nil {
		return nil, err
	}
	if err = os.Rename(stage, final); err != nil {
		return nil, fmt.Errorf("session %s already exists: %w", sessionID, err)
	}
	if opened.log, err = openEventLog(ctx, final); err != nil {
		return nil, err
	}
	if tail, err := opened.log.tail(); err != nil || tail != receipt.Tail {
		return nil, errors.Join(sdksession.ErrTail, err, opened.log.close())
	}
	return opened, nil
}
func newEventState() *eventState {
	return &eventState{batches: map[string]sdksession.Tail{}}
}

func (s *Store) openSession(ctx context.Context, id string) (*eventSession, error) {
	if err := transcript.ValidateStorageID(id); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.eventsPath(id)); err != nil {
		return nil, err
	}
	// Reject malformed complete product facts before SDK tail recovery can
	// modify an unfinished suffix. This also rejects a missing initial identity.
	state, err := s.readEvents(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	log, err := openEventLog(ctx, s.sessionDir(id))
	if err != nil {
		return nil, err
	}
	// Opening the SDK writer performs only its transport tail recovery.
	tail, err := log.tail()
	if err != nil {
		return nil, errors.Join(err, log.close())
	}
	if state.tail != tail {
		// A previous owner may have committed between the read and our claim.
		state, err = s.readEvents(ctx, id, nil)
		if err != nil {
			return nil, errors.Join(err, log.close())
		}
	}
	if state.tail != tail {
		return nil, errors.Join(sdksession.ErrTail, log.close())
	}
	return &eventSession{id: id, path: s.eventsPath(id), log: log, state: state}, nil
}
func (s *Store) replayEvents(ctx context.Context, id string, tailLimit *sdksession.Tail) (*Projection, sdksession.Tail, error) {
	state, err := s.readEvents(ctx, id, tailLimit)
	if err != nil {
		return nil, sdksession.Tail{}, err
	}
	return state.projection, state.tail, nil
}

// readEvents reads through the inclusive SDK batch tailLimit; nil reads all complete batches.
// A missing or changed tail is an error.
func (s *Store) readEvents(ctx context.Context, id string, tailLimit *sdksession.Tail) (*eventState, error) {
	return s.readEventsIn(ctx, s.sessionDir(id), id, tailLimit)
}

// readEventsIn reads the session whose directory is dir, live or trashed.
func (s *Store) readEventsIn(ctx context.Context, dir, id string, tailLimit *sdksession.Tail) (*eventState, error) {
	state := newEventState()
	for committed, err := range readEventBatches(ctx, dir) {
		if err != nil {
			return nil, err
		}
		b := committed.Batch
		if len(b.Records) != 1 || b.Records[0].Type != sdksession.TypeApp || b.Records[0].Name != "san.events" {
			return nil, errNotEvents
		}
		payload, err := decodeEventBatchPayload(b.Records[0].Data)
		if err != nil {
			return nil, err
		}
		if !b.Records[0].Time.Equal(time.UnixMilli(payload.Events[0].Time).UTC()) {
			return nil, fmt.Errorf("event record timestamp mismatch")
		}
		if err := rejectLocalEvidence(payload.Validation, id); err != nil {
			return nil, err
		}
		validation, err := mergeReplayValidation(state.validation, payload.Validation)
		if err != nil {
			return nil, err
		}
		state.projection, err = applyReplayEventBatch(state.projection, id, payload.Events, validation)
		if err != nil {
			return nil, err
		}
		if state.projection.data.Identity.ProjectID != s.projectID {
			return nil, fmt.Errorf("event project identity mismatch")
		}
		state.validation = validation
		state.batches[b.ID] = b.Expected
		state.tail = committed.Receipt.Tail
		if tailLimit != nil && state.tail.Sequence >= tailLimit.Sequence {
			if state.tail != *tailLimit {
				return nil, sdksession.ErrTail
			}
			break
		}
	}
	if tailLimit != nil && state.tail != *tailLimit {
		return nil, sdksession.ErrTail
	}
	if state.projection == nil {
		return nil, fmt.Errorf("missing session.create")
	}
	return state, nil
}

func (s *eventSession) prepareEventBatch(batchID string, events []Event, validation EventValidationContext) (*preparedEventBatch, error) {
	if s == nil {
		return nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.state == nil {
		return nil, errNotEvents
	}
	if batchID == "" {
		return nil, fmt.Errorf("empty batch ID")
	}
	raw, err := json.Marshal(eventBatchPayload{1, events, validation})
	if err != nil {
		return nil, err
	}
	payload, err := decodeEventBatchPayload(raw)
	if err != nil {
		return nil, err
	}
	state := s.state
	p := &preparedEventBatch{owner: s, batch: sdksession.Batch{ID: batchID, Expected: state.tail, Records: []sdksession.Record{{Type: sdksession.TypeApp, Name: "san.events", Time: time.UnixMilli(payload.Events[0].Time).UTC(), Data: raw}}}}
	// Reject deterministic transport timestamp encoding failures before Commit.
	if _, err := p.batch.Records[0].Time.MarshalJSON(); err != nil {
		return nil, err
	}
	// Retry content is judged by SDK, before current-tail-dependent validation.
	if expected, ok := state.batches[batchID]; ok {
		p.batch.Expected = expected
		return p, nil
	}
	if err := rejectLocalEvidence(payload.Validation, s.id); err != nil {
		return nil, err
	}
	p.validation, err = mergeValidation(state.validation, payload.Validation)
	if err != nil {
		return nil, err
	}
	p.projection, err = ApplyEventBatch(state.projection, s.id, payload.Events, p.validation)
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (s *eventSession) commitEventBatch(ctx context.Context, p *preparedEventBatch) (sdksession.Receipt, error) {
	if s == nil {
		return sdksession.Receipt{}, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return sdksession.Receipt{}, ErrClosed
	}
	if s.err != nil {
		return sdksession.Receipt{}, s.err
	}
	if p == nil || p.owner != s || s.state == nil {
		return sdksession.Receipt{}, fmt.Errorf("foreign prepared batch")
	}
	state := s.state
	_, retry := state.batches[p.batch.ID]
	if !retry && p.batch.Expected != state.tail {
		return sdksession.Receipt{}, sdksession.ErrTail
	}
	receipt, err := s.log.commit(ctx, p.batch)
	if err != nil {
		if !errors.Is(err, sdksession.ErrConflict) && !errors.Is(err, sdksession.ErrTail) && !errors.Is(err, sdksession.ErrMode) {
			s.err = err
		}
		return receipt, err
	}
	if !retry {
		state.projection = p.projection
		state.validation = p.validation
		state.tail = receipt.Tail
		state.batches[p.batch.ID] = p.batch.Expected
	}
	return receipt, nil
}
func (s *eventSession) eventSnapshot() (*Projection, error) {
	if s == nil {
		return nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.state == nil {
		return nil, errNotEvents
	}
	// Projection is immutable and its public accessors detach their results.
	return s.state.projection, nil
}

// rejectLocalEvidence refuses evidence that claims a foreign source inside this
// session: its own events are resolved from the committed prefix, never evidence.
func rejectLocalEvidence(v EventValidationContext, session string) error {
	for key := range v.ForeignEvents {
		if s, _, _ := parseForeignKey(key); s == session {
			return fmt.Errorf("foreign evidence contradicts local identity")
		}
	}
	return nil
}
