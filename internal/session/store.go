package session

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/genai-io/san/internal/transcript"
)

// Store is one project's typed sessions: a directory per session under
// sessions/, with their trash, cache and listings. It knows the project it
// belongs to and nothing about the runtime or the current selection.
type Store struct {
	cwd        string
	projectID  string
	projectDir string
	project    *projectIdentity
	workspace  string
	closed     atomic.Bool
}

// OpenStore identifies the project containing startDir and opens its store.
func OpenStore(ctx context.Context, startDir string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	project, err := resolveProject(ctx, home, startDir)
	if err != nil {
		return nil, err
	}
	return &Store{cwd: project.InitialCwd, projectID: project.ID, projectDir: project.Root, project: &project, workspace: project.Workspace}, nil
}

// openStoreIn opens a store rooted at dir with dir itself as the workspace. It
// skips project resolution, for tests and tools that already own the directory.
func openStoreIn(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	return &Store{cwd: dir, projectID: ProjectID(dir), projectDir: dir, workspace: dir}, nil
}

// Close prevents new operations. Sessions opened from the store must be closed first.
func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

func (s *Store) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (s *Store) checkID(ctx context.Context, id string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	return transcript.ValidateStorageID(id)
}

// CreateOptions fixes a session's identity. Omitted values use the store's
// workspace and startup directory, agent "main", and a generated ID.
type CreateOptions struct {
	ID         string
	Workspace  string
	InitialCWD string
	AgentID    string
	Config     *EventConfig
	Parent     *ParentLink
	CreatedAt  time.Time
}

// Create commits the identity before returning its writer handle.
func (s *Store) Create(ctx context.Context, opts CreateOptions) (*Session, error) {
	if opts.ID == "" {
		opts.ID = NewSessionID()
	}
	if err := s.checkID(ctx, opts.ID); err != nil {
		return nil, err
	}
	if opts.Workspace == "" {
		opts.Workspace = s.workspace
	}
	if opts.InitialCWD == "" {
		opts.InitialCWD = s.cwd
	}
	if opts.AgentID == "" {
		opts.AgentID = "main"
	}
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = time.Now()
	}
	validation := EventValidationContext{}
	if opts.Parent != nil {
		if err := transcript.ValidateStorageID(opts.Parent.Session); err != nil {
			return nil, err
		}
		parent, err := s.readEvents(ctx, opts.Parent.Session, nil)
		if err != nil {
			return nil, err
		}
		ancestors := []string{opts.Parent.Session}
		if link := parent.projection.Identity().Parent; link != nil {
			ancestors = append(ancestors, parent.validation.ParentAncestors[link.Session]...)
		}
		validation.ParentAncestors = map[string][]string{opts.Parent.Session: ancestors}
	}
	create := Event{Seq: 1, Type: EventSessionCreate, SchemaVersion: 1, Time: opts.CreatedAt.UnixMilli(),
		Payload: SessionCreatePayload{ProjectID: s.projectID, Workspace: opts.Workspace,
			InitialCWD: opts.InitialCWD, AgentID: opts.AgentID, Config: opts.Config, Parent: opts.Parent}}
	var writer *eventSession
	err := (library{root: s.projectDir}).withLock(ctx, opts.ID, func() error {
		var err error
		writer, err = s.createSession(ctx, "create", opts.ID, create, validation)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Session{store: s, writer: writer}, nil
}

// Open validates the committed prefix, then claims the existing writer.
// It preserves stored identity and never restores permissions or executes work.
func (s *Store) Open(ctx context.Context, id string) (*Session, error) {
	if err := s.checkID(ctx, id); err != nil {
		return nil, err
	}
	var writer *eventSession
	err := (library{root: s.projectDir}).withLock(ctx, id, func() error {
		var err error
		writer, err = s.openSession(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Session{store: s, writer: writer}, nil
}

// ForkOptions selects an inclusive event prefix; Seq zero copies the whole log.
type ForkOptions struct {
	ID  string // generated when omitted
	Seq uint64
}

func (s *Store) Fork(ctx context.Context, id string, opts ForkOptions) (*Session, error) {
	if err := s.checkID(ctx, id); err != nil {
		return nil, err
	}
	if opts.ID == "" {
		opts.ID = NewSessionID()
	}
	var writer *eventSession
	err := (library{root: s.projectDir}).withLock(ctx, opts.ID, func() error {
		var err error
		writer, err = s.forkSession(ctx, id, opts.ID, opts.Seq, time.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Session{store: s, writer: writer}, nil
}

// Get reads a live session's generated overview without taking its writer.
// User-chosen titles and other attributes are available on ListItem.
func (s *Store) Get(ctx context.Context, id string) (Overview, error) {
	if err := s.checkID(ctx, id); err != nil {
		return Overview{}, err
	}
	return s.overview(ctx, id)
}

func (s *Store) List(ctx context.Context, query ListQuery) (ListPage, error) {
	if err := s.check(ctx); err != nil {
		return ListPage{}, err
	}
	return s.listOverviews(ctx, query)
}

// SetAttrs changes user intent without taking the conversation's writer lock.
func (s *Store) SetAttrs(ctx context.Context, id string, patch AttrsPatch) error {
	if err := s.checkID(ctx, id); err != nil {
		return err
	}
	return (library{root: s.projectDir}).withLock(ctx, id, func() error {
		if _, err := os.Stat(s.eventsPath(id)); err != nil {
			return err
		}
		_, err := updateAttrs(s.sessionDir(id), patch)
		return err
	})
}

// Delete moves a closed, unpinned session to trash; it does not purge it.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := s.checkID(ctx, id); err != nil {
		return err
	}
	return (library{root: s.projectDir}).trash(ctx, id, time.Now())
}

func (s *Store) Restore(ctx context.Context, id string) error {
	if err := s.checkID(ctx, id); err != nil {
		return err
	}
	return (library{root: s.projectDir}).restore(ctx, id)
}

func (s *Store) Clean(ctx context.Context, opts CleanOptions) (CleanResult, error) {
	if err := s.check(ctx); err != nil {
		return CleanResult{}, err
	}
	for _, id := range opts.IDs {
		if err := transcript.ValidateStorageID(id); err != nil {
			return CleanResult{}, err
		}
	}
	return s.clean(ctx, opts)
}
