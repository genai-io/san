package session

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"

	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

// ProjectDir is the registered project's storage root, not its workspace.
func (s *Store) ProjectDir() string { return s.projectDir }

// Attrs reads authoritative user intent without locks or cache writes.
// Missing attributes mean defaults; malformed attributes remain an error.
func (s *Store) Attrs(ctx context.Context, id string) (Attrs, error) {
	if err := s.checkID(ctx, id); err != nil {
		return Attrs{}, err
	}
	if _, err := os.Stat(s.eventsPath(id)); err != nil {
		return Attrs{}, err
	}
	return readAttrs(s.sessionDir(id))
}

// Read projects the committed prefix without claiming a writer or repairing files.
func (s *Store) Read(ctx context.Context, id string) (Projection, error) {
	if err := s.checkID(ctx, id); err != nil {
		return Projection{}, err
	}
	state, err := s.readEvents(ctx, id, nil)
	if err != nil {
		return Projection{}, err
	}
	return *state.projection, nil
}

// ContextRequest returns native active context at an inclusive event sequence.
// Sequence zero selects the latest committed prefix. Full history is on Projection.Messages.
func (s *Store) ContextRequest(ctx context.Context, id string, seq uint64) (*ai.Request, error) {
	if err := s.checkID(ctx, id); err != nil {
		return nil, err
	}
	state, err := s.readEvents(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	p := state.projection
	if seq != 0 {
		if seq > uint64(len(p.data.Events)) {
			return nil, fmt.Errorf("event sequence out of range")
		}
		p, err = ReplayEventBatches(id, [][]Event{p.data.Events[:seq]}, state.validation)
		if err != nil {
			return nil, err
		}
	}
	return conversationRequest(p, nil), nil
}

// InferenceRequest rebuilds an admitted request without opening its session writer.
func (s *Store) InferenceRequest(ctx context.Context, id string, seq uint64) (*ai.Request, error) {
	if err := s.checkID(ctx, id); err != nil {
		return nil, err
	}
	state, err := s.readEvents(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	return reconstructInferenceRequest(id, *state.projection, seq, blobStore{dir: filepath.Join(s.sessionDir(id), "blobs")}, state.validation)
}

// CommittedBatches exposes checked SDK batches even when their application payload
// is newer than this binary. It does not validate typed domain events or repair files.
func (s *Store) CommittedBatches(ctx context.Context, id string) iter.Seq2[sdksession.CommittedBatch, error] {
	return func(yield func(sdksession.CommittedBatch, error) bool) {
		if err := s.checkID(ctx, id); err != nil {
			yield(sdksession.CommittedBatch{}, err)
			return
		}
		readEventBatches(ctx, s.sessionDir(id))(yield)
	}
}
