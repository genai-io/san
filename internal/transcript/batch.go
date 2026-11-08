package transcript

import (
	"context"
	"errors"
	"iter"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

var (
	// ErrUnclaimed means a batch operation has no claimed writer.
	ErrUnclaimed = errors.New("transcript: writer is not claimed")
	// ErrEventFormat means a legacy operation cannot read the batch format.
	ErrEventFormat = errors.New("transcript: SDK batch format requires the event reader")
)

// CommitBatch commits through the current lease without acquiring a writer.
func (s *Store) CommitBatch(ctx context.Context, id string, batch sdksession.Batch) (sdksession.Receipt, error) {
	if err := ValidateStorageID(id); err != nil {
		return sdksession.Receipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return sdksession.Receipt{}, err
	}
	w := s.writers[id]
	if w == nil {
		return sdksession.Receipt{}, ErrUnclaimed
	}
	receipt, err := w.Commit(ctx, batch)
	// A failed commit may still have written bytes.
	delete(s.sessions, id)
	return receipt, err
}

// BatchTail returns the checked tail of the current claimed writer.
func (s *Store) BatchTail(ctx context.Context, id string) (sdksession.Tail, error) {
	if err := ValidateStorageID(id); err != nil {
		return sdksession.Tail{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return sdksession.Tail{}, err
	}
	w := s.writers[id]
	if w == nil {
		return sdksession.Tail{}, ErrUnclaimed
	}
	return w.Tail()
}

// Batches reads whole validated SDK batches without claiming or repairing the log.
func (s *Store) Batches(ctx context.Context, id string) iter.Seq2[sdksession.CommittedBatch, error] {
	return func(yield func(sdksession.CommittedBatch, error) bool) {
		if err := ValidateStorageID(id); err != nil {
			yield(sdksession.CommittedBatch{}, err)
			return
		}
		s.log.Batches(ctx, id)(yield)
	}
}

// CheckLegacyFormat rejects a batch log before a legacy caller claims its writer.
// It reads only the first record; it does not validate or repair the whole log.
func (s *Store) CheckLegacyFormat(ctx context.Context, id string) error {
	if err := ValidateStorageID(id); err != nil {
		return err
	}
	return s.legacyFormat(ctx, id)
}

// legacyFormat probes only the first record, preserving batch boundaries.
func (s *Store) legacyFormat(ctx context.Context, id string) error {
	for _, err := range s.log.Batches(ctx, id) {
		if errors.Is(err, sdksession.ErrMode) {
			return nil
		}
		if err != nil {
			return err
		}
		return ErrEventFormat
	}
	return nil
}
