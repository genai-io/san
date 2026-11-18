package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// forkSession copies the first atSeq events of a live session into a new
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
func (s *Store) forkSession(ctx context.Context, srcID, newID string, atSeq uint64, now time.Time) (*eventSession, error) {
	if err := ValidateStorageID(newID); err != nil {
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

	opened := &eventSession{id: newID, path: s.eventsPath(newID), state: newEventState()}
	return s.publishSession(ctx, opened, func(stage string) error {
		if err := s.copyForkBlobs(srcID, stage, prefix); err != nil {
			return err
		}
		// Carry the evidence the copied events were accepted under.
		validation := EventValidationContext{ForeignEvents: src.validation.ForeignEvents, ForkPrefixes: map[string]uint64{srcID: atSeq}}
		commit := func(id string, events []Event, validation EventValidationContext) error {
			p, err := opened.prepareEventBatch(id, events, validation)
			if err != nil {
				return err
			}
			_, err = opened.commitEventBatch(ctx, p)
			return err
		}
		if err := commit("fork-create", prefix[:1], validation); err != nil {
			return err
		}
		if atSeq > 1 {
			if err := commit("fork-copy", prefix[1:], EventValidationContext{}); err != nil {
				return err
			}
		}
		if opened.state.projection.State().GroupHint != nil {
			clear := Event{Seq: atSeq + 1, Type: EventSessionUpdate, SchemaVersion: 1, Time: now.UnixMilli(), Payload: SessionUpdatePayload{State: &StatePatch{GroupHint: ClearField[GroupHint]()}}}
			return commit("fork-group", []Event{clear}, EventValidationContext{})
		}
		return nil
	})
}

func (s *Store) copyForkBlobs(srcID, stage string, prefix []Event) error {

	srcBlobs := blobStore{dir: filepath.Join(s.sessionDir(srcID), "blobs")}
	dstBlobs := blobStore{dir: filepath.Join(stage, "blobs")}
	if err := copyDisplayAssets(srcBlobs.dir, dstBlobs.dir); err != nil {
		return err
	}
	for _, e := range prefix {
		in, ok := e.Payload.(*InferenceStartPayload)
		if !ok || in.Input.Standalone == "" {
			continue
		}
		data, err := srcBlobs.Get(in.Input.Standalone)
		if err != nil {
			return fmt.Errorf("fork needs request %s: %w", in.Input.Standalone, err)
		}
		if _, err := dstBlobs.Put(data); err != nil {
			return err
		}
	}
	return nil
}
