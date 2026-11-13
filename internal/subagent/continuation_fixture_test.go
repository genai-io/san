package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/session"
)

// continuationSessions uses real parent identities so child ParentLinks are validated.
func continuationSessions(t *testing.T, parentID string) (*session.Store, *session.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parent := continuationParent(t, store, parentID)
	return store, parent
}

func continuationParent(t *testing.T, store *session.Store, id string) *session.Session {
	t.Helper()
	parent, err := store.Create(context.Background(), session.CreateOptions{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	return parent
}

func bindContinuationSessions(t *testing.T, executor *Executor, store *session.Store, parent *session.Session) {
	t.Helper()
	if err := executor.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
}
