package session

// Private typed-event persistence. Ordinary Store.Create/Open remain schema 4.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
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
	ids        map[string]bool
}
type preparedEventBatch struct {
	owner      *Session
	batch      sdksession.Batch
	projection *Projection
	validation EventValidationContext
	ids        []string
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

func (s *Store) createEventSession(ctx context.Context, batchID string, create Event, validation EventValidationContext) (*Session, error) {
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
	if err := transcript.ValidateStorageID(create.SessionID); err != nil {
		return nil, err
	}
	opened := &Session{store: s.transcriptStore, sessionID: create.SessionID, eventState: newEventState()}
	prepared, err := opened.prepareEventBatch(batchID, []Event{create}, payload.Validation)
	if err != nil {
		return nil, err
	}
	if exists, err := s.transcriptStore.Exists(ctx, create.SessionID); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("session %s already exists", create.SessionID)
	}
	release, err := s.transcriptStore.ClaimContext(ctx, create.SessionID)
	if err != nil {
		return nil, err
	}
	opened.release = release
	// Claim may create an empty SDK file; only an empty file is eligible.
	info, err := os.Stat(s.TranscriptPath(create.SessionID))
	if err != nil || info.Size() != 0 {
		if err == nil {
			err = fmt.Errorf("session %s already exists", create.SessionID)
		}
		return nil, errors.Join(err, opened.Close())
	}
	if _, err = opened.commitEventBatch(ctx, prepared); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return opened, nil
}
func newEventState() *eventState {
	return &eventState{batches: map[string]sdksession.Tail{}, ids: map[string]bool{}}
}

func (s *Store) openEventSession(ctx context.Context, id string) (*Session, error) {
	if err := transcript.ValidateStorageID(id); err != nil {
		return nil, err
	}
	if exists, err := s.transcriptStore.Exists(ctx, id); err != nil {
		return nil, err
	} else if !exists {
		return nil, os.ErrNotExist
	}
	// Reject malformed complete product facts before SDK tail recovery can
	// modify an unfinished suffix. This also rejects a missing initial identity.
	state, err := s.readEvents(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	release, err := s.transcriptStore.ClaimContext(ctx, id)
	if err != nil {
		return nil, err
	}
	// Opening the SDK writer performs only its transport tail recovery.
	tail, err := s.transcriptStore.BatchTail(ctx, id)
	if err != nil {
		return nil, errors.Join(err, release())
	}
	if state.tail != tail {
		// A previous owner may have committed between the read and our claim.
		state, err = s.readEvents(ctx, id, nil)
		if err != nil {
			return nil, errors.Join(err, release())
		}
	}
	if state.tail != tail {
		return nil, errors.Join(sdksession.ErrTail, release())
	}
	return &Session{store: s.transcriptStore, sessionID: id, release: release, eventState: state}, nil
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
	state := newEventState()
	for committed, err := range s.transcriptStore.Batches(ctx, id) {
		if err != nil {
			return nil, err
		}
		b := committed.Batch
		if len(b.Records) != 1 || b.Records[0].Type != sdksession.TypeApp || b.Records[0].Name != "san.events" {
			return nil, transcript.ErrEventFormat
		}
		payload, err := decodeEventBatchPayload(b.Records[0].Data)
		if err != nil {
			return nil, err
		}
		if !b.Records[0].Time.Equal(time.UnixMilli(payload.Events[0].CreatedAt).UTC()) {
			return nil, fmt.Errorf("event record timestamp mismatch")
		}
		for foreignID, fact := range payload.Validation.ForeignEvents {
			if state.ids[foreignID] || fact.SessionID == id {
				return nil, fmt.Errorf("foreign evidence contradicts local identity")
			}
		}
		validation, err := mergeReplayValidation(state.validation, payload.Validation)
		if err != nil {
			return nil, err
		}
		for _, e := range payload.Events {
			if e.SessionID != id || state.ids[e.ID] {
				return nil, fmt.Errorf("event session or ID conflict")
			}
			state.ids[e.ID] = true
		}
		state.projection, err = applyReplayEventBatch(state.projection, payload.Events, validation)
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

func (s *Session) prepareEventBatch(batchID string, events []Event, validation EventValidationContext) (*preparedEventBatch, error) {
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
	if s.eventState == nil {
		return nil, transcript.ErrEventFormat
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
	state := s.eventState
	p := &preparedEventBatch{owner: s, batch: sdksession.Batch{ID: batchID, Expected: state.tail, Records: []sdksession.Record{{Type: sdksession.TypeApp, Name: "san.events", Time: time.UnixMilli(payload.Events[0].CreatedAt).UTC(), Data: raw}}}}
	// Reject deterministic transport timestamp encoding failures before Commit.
	if _, err := p.batch.Records[0].Time.MarshalJSON(); err != nil {
		return nil, err
	}
	// Retry content is judged by SDK, before current-tail-dependent validation.
	if expected, ok := state.batches[batchID]; ok {
		p.batch.Expected = expected
		return p, nil
	}
	for foreignID, fact := range payload.Validation.ForeignEvents {
		if state.ids[foreignID] || fact.SessionID == s.sessionID {
			return nil, fmt.Errorf("foreign evidence contradicts local identity")
		}
	}
	p.validation, err = mergeValidation(state.validation, payload.Validation)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range payload.Events {
		if e.SessionID != s.sessionID || state.ids[e.ID] || seen[e.ID] {
			return nil, fmt.Errorf("event session or ID conflict")
		}
		seen[e.ID] = true
		p.ids = append(p.ids, e.ID)
	}
	p.projection, err = ApplyEventBatch(state.projection, payload.Events, p.validation)
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (s *Session) commitEventBatch(ctx context.Context, p *preparedEventBatch) (sdksession.Receipt, error) {
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
	if p == nil || p.owner != s || s.eventState == nil {
		return sdksession.Receipt{}, fmt.Errorf("foreign prepared batch")
	}
	state := s.eventState
	_, retry := state.batches[p.batch.ID]
	if !retry && p.batch.Expected != state.tail {
		return sdksession.Receipt{}, sdksession.ErrTail
	}
	receipt, err := s.store.CommitBatch(ctx, s.sessionID, p.batch)
	if err != nil {
		if !errors.Is(err, sdksession.ErrConflict) && !errors.Is(err, sdksession.ErrTail) && !errors.Is(err, sdksession.ErrMode) && !errors.Is(err, transcript.ErrEventFormat) && !errors.Is(err, transcript.ErrUnclaimed) {
			s.err = err
		}
		return receipt, err
	}
	if !retry {
		state.projection = p.projection
		state.validation = p.validation
		state.tail = receipt.Tail
		state.batches[p.batch.ID] = p.batch.Expected
		for _, id := range p.ids {
			state.ids[id] = true
		}
	}
	return receipt, nil
}
func (s *Session) eventSnapshot() (*Projection, error) {
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
	if s.eventState == nil {
		return nil, transcript.ErrEventFormat
	}
	// Projection is immutable and its public accessors detach their results.
	return s.eventState.projection, nil
}
