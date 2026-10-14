package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func (s *Store) TranscriptPath(sessionID string) string {
	if s.transcriptStore != nil {
		return s.transcriptStore.TranscriptPath(sessionID)
	}
	return filepath.Join(s.projectDir, "transcripts", sessionID+".jsonl")
}

func (s *Store) List() ([]Summary, error) {
	return s.transcriptStore.List(context.Background(), s.projectID, transcript.ListOptions{})
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.transcriptStore.Delete(context.Background(), id); err != nil {
		return err
	}
	return errors.Join(os.RemoveAll(s.toolResultsDir(id)), os.RemoveAll(s.ImagesDir(id)))
}

func (s *Store) Read(id string) (*History, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, err := s.readHistory(context.Background(), id, false)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// ReadChatHistory reads the selected branch before compaction for display only.
func (s *Store) ReadChatHistory(id string) ([]core.ChatMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, err := s.readHistory(context.Background(), id, true)
	if err != nil {
		return nil, err
	}
	return s.ChatRows(snap), nil
}

// Create opens a new durable session. The caller owns it until Close.
func (s *Store) Create(opts CreateOptions) (*Session, error) {
	if opts.ID == "" {
		opts.ID = NewSessionID()
	}
	if err := transcript.ValidateStorageID(opts.ID); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.TranscriptPath(opts.ID)); err == nil {
		return nil, fmt.Errorf("session %s already exists", opts.ID)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if opts.Cwd == "" {
		opts.Cwd = s.cwd
	}
	return newSession(s.transcriptStore, opts)
}

// Open claims the session before reading and projecting its history once.
func (s *Store) Open(id string) (*Session, *History, error) {
	if err := transcript.ValidateStorageID(id); err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(s.TranscriptPath(id)); err != nil {
		return nil, nil, err
	}
	release, err := s.transcriptStore.Claim(id)
	if err != nil {
		return nil, nil, err
	}
	history, err := s.Read(id)
	if err != nil {
		return nil, nil, errors.Join(err, release())
	}
	opened, err := sessionFromLease(s.transcriptStore, CreateOptions{ID: id, AgentID: "main", Cwd: history.Metadata.Cwd}, release)
	if err != nil {
		return nil, nil, err
	}
	return opened, history, nil
}

// Fork returns an opened copy with its own identity, assets, and writer lease.
func (s *Store) Fork(sourceID string) (*Session, *History, error) {
	id, err := s.copyTranscript(sourceID)
	if err != nil {
		return nil, nil, err
	}
	opened, history, err := s.Open(id)
	if err != nil {
		return nil, nil, errors.Join(err, s.Delete(id))
	}
	// Group membership belongs to the source identity, not its conversation.
	if history.Metadata.Group != "" {
		at := time.Now()
		if err := opened.Append(context.Background(), transcript.Record{Time: at, Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Group: new("")}}); err != nil {
			return nil, nil, errors.Join(err, opened.Close(), s.Delete(id))
		}
		history.Metadata.Group = ""
		history.Metadata.UpdatedAt = at
	}
	return opened, history, nil
}

func (s *Store) copyTranscript(sourceID string) (string, error) {
	if err := transcript.ValidateStorageID(sourceID); err != nil {
		return "", err
	}
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
			return "", err
		}
		if err := os.CopyFS(paths[1], os.DirFS(paths[0])); err != nil {
			return "", fmt.Errorf("copy fork assets: %w", err)
		}
	}
	if err := s.transcriptStore.Fork(context.Background(), transcript.ForkCommand{
		SourceSessionID: sourceID,
		NewSessionID:    newID,
		Time:            time.Now(),
	}); err != nil {
		return "", err
	}
	committed = true
	return newID, nil
}

func (s *Store) PersistToolResult(sessionID, toolCallID, content string) error {
	if err := transcript.ValidateStorageID(sessionID); err != nil {
		return err
	}
	if err := transcript.ValidateStorageID(toolCallID); err != nil {
		return err
	}
	dir := s.toolResultsDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create tool result dir: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, toolCallID), []byte(content), 0o644)
}

// RecordSubagentConversation writes the completed child's native messages.
func (s *Store) RecordSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (id, path string, err error) {
	if len(messages) == 0 {
		return "", "", nil
	}
	if title == "" {
		title = "Subagent"
	}
	opened, err := s.Create(CreateOptions{AgentID: "subagent", Model: modelID, Cwd: cwd, ParentSessionID: parentSessionID})
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, opened.Close()) }()
	messages = slices.Clone(messages)
	for i := range messages {
		if messages[i].ID == "" {
			messages[i].ID = core.NewMessageID()
		}
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.ConversationReplaced, Messages: messages}); err != nil {
		return "", "", err
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Title: new(title)}}); err != nil {
		return "", "", err
	}
	return opened.ID(), opened.TranscriptPath(), nil
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

func (s *Store) readHistory(ctx context.Context, sessionID string, includeCompacted bool) (*History, error) {
	if s.transcriptStore == nil || sessionID == "" {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	load := s.transcriptStore.Load
	if includeCompacted {
		load = s.transcriptStore.LoadHistory
	}
	tx, err := load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load transcript %s: %w", sessionID, err)
	}
	if tx == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	sess := &History{
		Metadata:    transcript.MetadataFromTranscript(tx),
		Notices:     tx.Notices,
		ToolDetails: tx.ToolDetails,
		Messages:    tx.Messages,
		Tasks:       tx.State.Tasks,
	}

	if sess.Metadata.Title == "" {
		sess.Metadata.Title = GenerateTitle(sess.Messages)
	}
	if sess.Metadata.LastPrompt == "" {
		sess.Metadata.LastPrompt = ExtractLastUserText(sess.Messages)
	}
	return sess, nil
}
