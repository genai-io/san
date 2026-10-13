package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/transcript"
)

type Store struct {
	mu              sync.RWMutex
	cwd             string
	projectID       string
	projectDir      string
	transcriptStore *transcript.Store
}

func NewStore(cwd string) (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	projectID := ProjectID(cwd)
	projectDir := filepath.Join(confdir.Dir(homeDir), "projects", projectID)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create project directory: %w", err)
	}

	txStore, err := transcript.NewStore(projectDir, projectID)
	if err != nil {
		return nil, err
	}

	return &Store{
		cwd:             cwd,
		projectID:       projectID,
		projectDir:      projectDir,
		transcriptStore: txStore,
	}, nil
}

func NewStoreWithDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	txStore, err := transcript.NewStore(dir, ProjectID(dir))
	if err != nil {
		return nil, fmt.Errorf("create transcript store: %w", err)
	}
	return &Store{
		cwd:             dir,
		projectID:       ProjectID(dir),
		projectDir:      dir,
		transcriptStore: txStore,
	}, nil
}

func (s *Store) SessionPath(sessionID string) string {
	if s.transcriptStore != nil {
		return s.transcriptStore.TranscriptPath(sessionID)
	}
	return filepath.Join(s.projectDir, "transcripts", sessionID+".jsonl")
}

func (s *Store) List() ([]Summary, error) {
	return s.transcriptStore.List(context.Background(), s.projectID, transcript.ListOptions{})
}

func (s *Store) Latest() (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items, err := s.transcriptStore.List(context.Background(), s.projectID, transcript.ListOptions{})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return s.loadSnapshot(context.Background(), items[0].ID, false)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.transcriptStore.Delete(context.Background(), id); err != nil {
		return err
	}
	return errors.Join(os.RemoveAll(s.toolResultsDir(id)), os.RemoveAll(s.ImagesDir(id)))
}

func (s *Store) Load(id string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, err := s.loadSnapshot(context.Background(), id, false)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// LoadHistory reads the selected branch before compaction for display only.
func (s *Store) LoadHistory(id string) ([]core.ChatMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, err := s.loadSnapshot(context.Background(), id, true)
	if err != nil {
		return nil, err
	}
	return snap.ChatRows(), nil
}

// Save imports a full conversation snapshot and its product state.
func (s *Store) Save(sess *Snapshot) error {
	return s.save(sess, true)
}

// SaveState saves product state; the live writer owns message appends.
func (s *Store) SaveState(sess *Snapshot) error {
	return s.save(sess, false)
}

func (s *Store) save(sess *Snapshot, messages bool) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}

	gitBranch := getGitBranch(s.cwd)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.transcriptStore == nil {
		return fmt.Errorf("transcript store not configured")
	}

	now := time.Now()
	NormalizeMetadata(&sess.Metadata, sess.Messages, s.cwd, now)

	ctx := context.Background()
	id := sess.Metadata.ID
	if messages {
		release, err := s.transcriptStore.Claim(id)
		if err != nil {
			return err
		}
		defer release()
	}

	if err := s.transcriptStore.Start(ctx, transcript.StartCommand{
		SessionID:       id,
		Cwd:             sess.Metadata.Cwd,
		Provider:        sess.Metadata.Provider,
		Model:           sess.Metadata.Model,
		ParentSessionID: sess.Metadata.ParentSessionID,
		Time:            sess.Metadata.CreatedAt,
	}); err != nil {
		return err
	}

	if messages {
		for i := range sess.Messages {
			if sess.Messages[i].ID == "" {
				sess.Messages[i].ID = core.NewMessageID()
			}
		}
		if err := s.transcriptStore.Append(ctx, transcript.Record{SessionID: id, Time: now, Type: transcript.ConversationReplaced, Messages: sess.Messages}); err != nil {
			return err
		}
	}

	next := transcript.State{
		Title:      sess.Metadata.Title,
		LastPrompt: sess.Metadata.LastPrompt,
		Tag:        sess.Metadata.Tag,
		Mode:       sess.Metadata.Mode,
		AutoPilot:  sess.Metadata.AutoPilot,
		Group:      sess.Metadata.Group,
		Tasks:      todoSnapshots(sess.Tasks),
	}
	prev, err := s.transcriptStore.LoadState(ctx, id)
	if err != nil {
		return err
	}

	ops := transcript.StateOpsDiff(prev, next)
	if err := s.transcriptStore.Append(ctx, transcript.Record{SessionID: id, Time: now, GitBranch: gitBranch, Type: transcript.SessionStatePatched, State: &transcript.StateRecord{Ops: ops}}); err != nil {
		return err
	}
	// Refresh only the summaries changed by this save.
	if err := s.transcriptStore.Flush(); err != nil {
		return err
	}
	return nil
}

// Flush refreshes derived summaries; transcript writes are already durable.
func (s *Store) Flush() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.transcriptStore == nil {
		return nil
	}
	return s.transcriptStore.Flush()
}

func (s *Store) Fork(sourceID string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newID := NewSessionID()
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(s.toolResultsDir(newID))
			_ = os.RemoveAll(s.ImagesDir(newID))
		}
	}()
	for _, paths := range [][2]string{{s.toolResultsDir(sourceID), s.toolResultsDir(newID)}, {s.ImagesDir(sourceID), s.ImagesDir(newID)}} {
		if _, err := os.Stat(paths[0]); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		if err := os.CopyFS(paths[1], os.DirFS(paths[0])); err != nil {
			return nil, fmt.Errorf("copy fork assets: %w", err)
		}
	}
	if err := s.transcriptStore.Fork(context.Background(), transcript.ForkCommand{
		SourceSessionID: sourceID,
		NewSessionID:    newID,
		Time:            time.Now(),
	}); err != nil {
		return nil, err
	}
	committed = true
	forked, err := s.loadSnapshot(context.Background(), newID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load forked session: %w", err)
	}
	return forked, nil
}

func (s *Store) PersistToolResult(sessionID, toolCallID, content string) error {
	// Sanitize both sessionID and toolCallID to prevent path traversal
	safeSessionID := filepath.Base(sessionID)
	if safeSessionID == "." || safeSessionID == "/" || safeSessionID == "" {
		return fmt.Errorf("invalid session ID: %q", sessionID)
	}
	safeName := filepath.Base(toolCallID)
	if safeName == "." || safeName == "/" || safeName == "" {
		return fmt.Errorf("invalid tool call ID: %q", toolCallID)
	}
	dir := s.toolResultsDir(safeSessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create tool result dir: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, safeName), []byte(content), 0o644)
}

func (s *Store) SaveSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (string, string, error) {
	if len(messages) == 0 {
		return "", "", nil
	}
	if title == "" {
		title = "Subagent"
	}
	if cwd == "" {
		cwd = s.cwd
	}

	sess := &Snapshot{
		Metadata: Metadata{
			Title:           title,
			Model:           modelID,
			Cwd:             cwd,
			ParentSessionID: parentSessionID,
		},
		// A subagent has no interface, so its transcript is its conversation
		// with nothing drawn on it — which is what ToChat gives.
		Messages: messages,
	}
	if err := s.Save(sess); err != nil {
		return "", "", err
	}
	return sess.Metadata.ID, s.SessionPath(sess.Metadata.ID), nil
}

func (s *Store) toolResultsDir(sessionID string) string {
	return filepath.Join(s.projectDir, "blobs", "tool-result", sessionID)
}

// ImagesDir is where a session's pasted images are written when a text-only
// model has to be handed a path instead of the picture. That path is inlined
// into a turn the transcript replays, so the file has to live as long as the
// transcript does — next to the session's spilled tool results, gone with
// them in Delete.
func (s *Store) ImagesDir(sessionID string) string {
	return filepath.Join(s.projectDir, "blobs", "image", sessionID)
}

func (s *Store) loadSnapshot(ctx context.Context, sessionID string, history bool) (*Snapshot, error) {
	if s.transcriptStore == nil || sessionID == "" {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	load := s.transcriptStore.Load
	if history {
		load = s.transcriptStore.LoadHistory
	}
	tx, err := load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load transcript %s: %w", sessionID, err)
	}
	if tx == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	transcript.HydrateToolResultNodes(tx.ID, tx.Messages, func(toolCallID string) (string, error) {
		data, err := os.ReadFile(filepath.Join(s.toolResultsDir(tx.ID), toolCallID))
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	sess := &Snapshot{
		Metadata:    transcript.MetadataFromTranscript(tx),
		Notices:     tx.Notices,
		ToolDetails: tx.ToolDetails,
		Messages:    make([]core.Message, len(tx.Messages)),
		Tasks:       todoItems(tx.State.Tasks),
	}

	for i, n := range tx.Messages {
		sess.Messages[i] = n.Message
	}

	if sess.Metadata.Title == "" {
		sess.Metadata.Title = GenerateTitle(sess.Messages)
	}
	if sess.Metadata.LastPrompt == "" {
		sess.Metadata.LastPrompt = ExtractLastUserText(sess.Messages)
	}
	return sess, nil
}
