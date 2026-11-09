package session

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

// A session is one directory under sessions/. Its events are an sdk-go store
// opened on that directory with the fixed ID "events", so the batch format,
// writer lock and crash recovery stay sdk-go's and the whole directory can be
// renamed, copied or removed as one unit.
const (
	sessionsDirName = "sessions"
	eventsID        = "events"
	eventsFileName  = eventsID + ".jsonl"
)

func (s *Store) sessionsRoot() string { return filepath.Join(s.projectDir, sessionsDirName) }
func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.sessionsRoot(), id)
}
func (s *Store) eventsPath(id string) string { return filepath.Join(s.sessionDir(id), eventsFileName) }

// eventLog is one session directory's events file held by its single writer.
type eventLog struct{ w *sdksession.Writer }

// openEventLog claims the directory's writer, creating the events file if it is
// new. A second writer, in this or another process, gets sdksession.ErrBusy.
func openEventLog(ctx context.Context, dir string) (*eventLog, error) {
	st, err := sdksession.Open(dir)
	if err != nil {
		return nil, err
	}
	w, err := st.Writer(ctx, eventsID)
	if err != nil {
		return nil, err
	}
	return &eventLog{w}, nil
}

func (l *eventLog) tail() (sdksession.Tail, error) { return l.w.Tail() }
func (l *eventLog) commit(ctx context.Context, b sdksession.Batch) (sdksession.Receipt, error) {
	return l.w.Commit(ctx, b)
}
func (l *eventLog) close() error { return l.w.Close() }

// readEventBatches reads whole checked batches without claiming the writer or
// repairing anything. A directory without events is os.ErrNotExist.
func readEventBatches(ctx context.Context, dir string) iter.Seq2[sdksession.CommittedBatch, error] {
	return func(yield func(sdksession.CommittedBatch, error) bool) {
		if _, err := os.Stat(filepath.Join(dir, eventsFileName)); err != nil {
			yield(sdksession.CommittedBatch{}, err)
			return
		}
		st, err := sdksession.Open(dir)
		if err != nil {
			yield(sdksession.CommittedBatch{}, err)
			return
		}
		st.Batches(ctx, eventsID)(yield)
	}
}

var errNotEvents = fmt.Errorf("session: events file holds records this reader does not know")
