package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/genai-io/san/internal/transcript"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

// forkEventSession copies the first atSeq events of a live session into a new
// session newID (atSeq 0 = everything committed). The copy keeps positions and
// native message IDs, so no reference is rewritten, and stays independent of
// its source: deleting the source never breaks it, and operations unfinished at
// the cut stay unfinished.
//
// Only the creation event changes: it records the fork and drops the parent
// link, since a fork is not a child. A group hint is cleared by one trailing
// session.update. Blobs the copied events reference are copied and verified;
// a missing or damaged blob fails the fork. Like Create, the fork is built in a
// staging directory and published by one rename.
func (s *Store) forkEventSession(ctx context.Context, srcID, newID string, atSeq uint64, now time.Time) (*eventSession, error) {
	if err := transcript.ValidateStorageID(newID); err != nil {
		return nil, err
	}
	src, err := s.readEvents(ctx, srcID, nil)
	if err != nil {
		return nil, err
	}
	all := src.projection.data.Events
	if atSeq == 0 {
		atSeq = uint64(len(all))
	}
	if atSeq < 1 || atSeq > uint64(len(all)) {
		return nil, fmt.Errorf("fork point %d is outside the %d committed events", atSeq, len(all))
	}
	prefix := eventCopy(all[:atSeq])
	create, ok := prefix[0].Payload.(*SessionCreatePayload)
	if !ok {
		return nil, errors.New("first event is not session.create")
	}
	create.Fork = &ForkLink{Session: srcID, Seq: atSeq}
	create.Parent = nil
	prefix[0].Time = now.UnixMilli()

	final := s.sessionDir(newID)
	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("session %s already exists", newID)
	}
	if err := os.MkdirAll(s.sessionsRoot(), 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(s.sessionsRoot(), "."+newID+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage) // a no-op once renamed

	srcBlobs := blobStore{dir: filepath.Join(s.sessionDir(srcID), "blobs")}
	dstBlobs := blobStore{dir: filepath.Join(stage, "blobs")}
	for _, e := range prefix {
		in, ok := e.Payload.(*InferenceStartPayload)
		if !ok || in.Input.Standalone == "" {
			continue
		}
		data, err := srcBlobs.Get(in.Input.Standalone)
		if err != nil {
			return nil, fmt.Errorf("fork needs request %s: %w", in.Input.Standalone, err)
		}
		if _, err := dstBlobs.Put(data); err != nil {
			return nil, err
		}
	}

	// Evidence a copied event was accepted under travels with it, and the fork
	// point is the prefix this fork was validated against.
	validation := EventValidationContext{ForeignEvents: src.validation.ForeignEvents, ForkPrefixes: map[string]uint64{srcID: atSeq}}
	opened := &eventSession{id: newID, path: s.eventsPath(newID), state: newEventState()}
	first, err := opened.prepareEventBatch("fork-create", prefix[:1], validation)
	if err != nil {
		return nil, err
	}
	if opened.log, err = openEventLog(ctx, stage); err != nil {
		return nil, err
	}
	closeLog := func(err error) error { return errors.Join(err, opened.log.close()) }
	receipt, err := opened.commitEventBatch(ctx, first)
	if err != nil {
		return nil, closeLog(err)
	}
	if atSeq > 1 {
		rest, err := opened.prepareEventBatch("fork-copy", prefix[1:], EventValidationContext{})
		if err != nil {
			return nil, closeLog(err)
		}
		if receipt, err = opened.commitEventBatch(ctx, rest); err != nil {
			return nil, closeLog(err)
		}
	}
	if opened.state.projection.State().GroupHint != nil {
		clear := Event{Seq: atSeq + 1, Type: EventSessionUpdate, SchemaVersion: 1, Time: now.UnixMilli(),
			Payload: SessionUpdatePayload{State: &StatePatch{GroupHint: ClearField[GroupHint]()}}}
		p, err := opened.prepareEventBatch("fork-group", []Event{clear}, EventValidationContext{})
		if err != nil {
			return nil, closeLog(err)
		}
		if receipt, err = opened.commitEventBatch(ctx, p); err != nil {
			return nil, closeLog(err)
		}
	}
	if err := opened.log.close(); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, final); err != nil {
		return nil, fmt.Errorf("session %s already exists: %w", newID, err)
	}
	if opened.log, err = openEventLog(ctx, final); err != nil {
		return nil, err
	}
	if tail, err := opened.log.tail(); err != nil || tail != receipt.Tail {
		return nil, errors.Join(sdksession.ErrTail, err, opened.log.close())
	}
	return opened, nil
}
