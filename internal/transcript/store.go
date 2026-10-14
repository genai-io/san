package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"
)

type Store struct {
	mu        sync.RWMutex
	baseDir   string
	projectID string

	// sessions holds per-transcript caches keyed by session ID. Populated
	// lazily on first access via warmupCachesLocked, which does one forward
	// scan and fills every field together so resumed sessions pay one O(N)
	// scan per writer lease. Claim and Delete evict all derived state
	// at once.
	sessions map[string]*sessionCache

	writers   map[string]*writerLock
	summaries map[string]summaryCache
}

// sessionCache holds the derived state we keep in memory to avoid re-scanning
// the transcript on every write. All fields are mu-guarded.
type sessionCache struct {
	warmed        bool                // forward scan completed
	persistedIDs  map[string]struct{} // committed message IDs
	lastGitBranch string              // sparse-emit base
	leafMessageID string              // parent pointer for next message
	repaired      bool
	inference     int
	exchange      int
	messageIDs    []string // active immutable message identities
	latestState   State    // projected state (cold-Save diff base)
}

func NewStore(baseDir, projectID string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(baseDir, "transcripts"), 0o755); err != nil {
		return nil, fmt.Errorf("create transcripts dir: %w", err)
	}
	return &Store{baseDir: baseDir, projectID: projectID}, nil
}

func (s *Store) Start(ctx context.Context, cmd StartCommand) error {
	if err := ValidateStorageID(cmd.SessionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	path := s.transcriptPath(cmd.SessionID)
	exists, err := fileExists(path)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	rec := Record{
		ID:        cmd.SessionID + ":start",
		SessionID: cmd.SessionID,
		Time:      cmd.Time,
		Type:      SessionStarted,
		Cwd:       cmd.Cwd,
		Version:   SchemaVersion,
		Start: &SessionStart{
			Provider:        cmd.Provider,
			Model:           cmd.Model,
			MaxTokens:       cmd.MaxTokens,
			AgentID:         cmd.AgentID,
			ParentSessionID: cmd.ParentSessionID,
		},
	}
	if err := s.appendRecord(path, rec, true); err != nil {
		return err
	}
	return nil
}

// Append commits one event and advances derived state only after the write succeeds.
func (s *Store) Append(ctx context.Context, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateStorageID(rec.SessionID); err != nil {
		return err
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	if err := s.warmupCachesLocked(rec.SessionID); err != nil {
		return err
	}
	c := s.cacheLocked(rec.SessionID)
	nextState := c.latestState
	switch rec.Type {
	case MessageAppended:
		if rec.Message == nil || rec.Message.ID == "" {
			return fmt.Errorf("message ID and payload are required")
		}
		if _, ok := c.persistedIDs[rec.Message.ID]; ok {
			return nil
		}
		if rec.ParentMessageID == "" {
			rec.ParentMessageID = c.leafMessageID
		}
		rec.ID = rec.SessionID + ":" + rec.Message.ID
		if rec.GitBranch == c.lastGitBranch {
			rec.GitBranch = ""
		}
	case ConversationReplaced:
		if err := validateMessages(rec.Messages); err != nil {
			return err
		}
		// Message IDs name immutable content; replaying the same chain is a no-op.
		if sameMessageIDs(c.messageIDs, rec.Messages) {
			return nil
		}
	case SessionStatePatched:
		if rec.State == nil {
			return fmt.Errorf("state payload is required")
		}
		patch, err := rec.State.changesFrom(c.latestState)
		if err != nil {
			return err
		}
		if patch == nil {
			if rec.GitBranch == "" || rec.GitBranch == c.lastGitBranch {
				return nil
			}
			patch = &StatePatch{}
		}
		rec.State = patch
		patch.applyTo(&nextState)

	case InferenceRequested, InferenceResponded, InferenceFailed:
		if rec.Inference == nil {
			return fmt.Errorf("inference payload is required")
		}
	case ToolAdded, ToolRemoved:
		if rec.Tool == nil {
			return fmt.Errorf("tool payload is required")
		}
	case SystemSectionAdded, SystemSectionRemoved:
		if rec.System == nil {
			return fmt.Errorf("system payload is required")
		}
	case ToolCompleted:
		if rec.ToolDetails == nil || rec.ToolDetails.ToolCallID == "" {
			return fmt.Errorf("tool details and call ID are required")
		}
	case NoticeAdded:
		if rec.Notice == nil {
			return fmt.Errorf("notice payload is required")
		}
	case HookFired:
		if rec.Hook == nil {
			return fmt.Errorf("hook payload is required")
		}
	case SkillStateChanged:
		if rec.Skill == nil {
			return fmt.Errorf("skill payload is required")
		}
	case PermissionRequired, PermissionDecided:
		if rec.Permission == nil {
			return fmt.Errorf("permission payload is required")
		}
	case SessionCompacted:
		if rec.Compaction == nil || rec.Compaction.MessageID == "" {
			return fmt.Errorf("compaction boundary is required")
		}
	default:
		return fmt.Errorf("unknown transcript event %q", rec.Type)
	}
	if rec.GitBranch == c.lastGitBranch {
		rec.GitBranch = ""
	}
	if rec.ID == "" {
		rec.ID = fmt.Sprintf("%s:%s:%d", rec.SessionID, rec.Type, rec.Time.UnixNano())
	}
	durable := rec.Type == MessageAppended || rec.Type == ConversationReplaced || rec.Type == SessionCompacted || rec.Type == SessionStatePatched || rec.Type == InferenceResponded || rec.Type == InferenceFailed
	if err := s.appendRecord(s.transcriptPath(rec.SessionID), rec, durable); err != nil {
		return err
	}
	if rec.GitBranch != "" {
		c.lastGitBranch = rec.GitBranch
	}
	if rec.Message != nil {
		c.persistedIDs[rec.Message.ID] = struct{}{}
		c.leafMessageID = rec.Message.ID
		c.messageIDs = append(c.messageIDs, rec.Message.ID)
	}
	if rec.Type == ConversationReplaced {
		c.messageIDs = make([]string, 0, len(rec.Messages))
		c.leafMessageID = ""
		for _, m := range rec.Messages {
			c.messageIDs = append(c.messageIDs, m.ID)
			c.persistedIDs[m.ID] = struct{}{}
			c.leafMessageID = m.ID
		}
	}
	c.latestState = nextState
	if rec.Inference != nil {
		c.inference = max(c.inference, rec.Inference.Sequence)
		c.exchange = max(c.exchange, rec.Inference.Exchange)
	}
	if rec.Type == SessionCompacted {
		records, err := s.loadRecordsLocked(s.transcriptPath(rec.SessionID))
		if err != nil {
			return err
		}
		tx, err := Project(records)
		if err != nil {
			return err
		}
		s.seedCacheLocked(rec.SessionID, records, tx)
		return nil
	}
	return nil
}

func (s *Store) Fork(ctx context.Context, cmd ForkCommand) error {
	if err := ValidateStorageID(cmd.SourceSessionID); err != nil {
		return err
	}
	if err := ValidateStorageID(cmd.NewSessionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	sourcePath := s.transcriptPath(cmd.SourceSessionID)
	records, err := s.loadRecordsLocked(sourcePath)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("source transcript not found: %s", cmd.SourceSessionID)
	}

	destPath := s.transcriptPath(cmd.NewSessionID)
	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open fork transcript: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, rec := range records {
		if rec.Message != nil {
			s.rewriteAttachments(rec.Message.Content, cmd.SourceSessionID, cmd.NewSessionID)
		}
		for i := range rec.Messages {
			s.rewriteAttachments(rec.Messages[i].Content, cmd.SourceSessionID, cmd.NewSessionID)
		}

		rec.SessionID = cmd.NewSessionID
		rec.ID = rewriteRecordSessionID(rec.ID, cmd.SourceSessionID, cmd.NewSessionID)
		if err := enc.Encode(rec); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("write fork record: %w", err)
		}
	}
	forkRec := Record{
		ID:        fmt.Sprintf("%s:fork:%d", cmd.NewSessionID, cmd.Time.UnixNano()),
		SessionID: cmd.NewSessionID,
		Time:      cmd.Time,
		Type:      SessionForked,
		Fork: &SessionFork{
			ParentSessionID: cmd.SourceSessionID,
		},
	}
	if err := enc.Encode(forkRec); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write fork marker: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close fork transcript: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename fork transcript: %w", err)
	}
	return nil
}

func (s *Store) rewriteAttachments(content ai.Content, beforeID, afterID string) {
	before := "[Full output persisted to blobs/tool-result/" + beforeID + "/"
	after := "[Full output persisted to blobs/tool-result/" + afterID + "/"
	beforeImage := filepath.Join(s.baseDir, "blobs", "image", beforeID) + string(os.PathSeparator)
	afterImage := filepath.Join(s.baseDir, "blobs", "image", afterID) + string(os.PathSeparator)
	for i := range content {
		if content[i].Type == ai.BlockText {
			content[i].Text = strings.ReplaceAll(content[i].Text, beforeImage, afterImage)
		}
		if content[i].ToolResult == nil {
			continue
		}
		for j := range content[i].ToolResult.Content {
			block := &content[i].ToolResult.Content[j]
			if block.Type == ai.BlockText {
				block.Text = strings.ReplaceAll(block.Text, before, after)
			}
		}
	}
}

func rewriteRecordSessionID(id, oldSessionID, newSessionID string) string {
	prefix := oldSessionID + ":"
	if after, ok := strings.CutPrefix(id, prefix); ok {
		return newSessionID + ":" + after
	}
	return id
}

func (s *Store) Load(ctx context.Context, transcriptID string) (*Transcript, error) {
	if err := ValidateStorageID(transcriptID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	records, err := s.loadRecordsLocked(s.transcriptPath(transcriptID))
	if err != nil {
		return nil, err
	}
	tx, err := Project(records)
	if err != nil {
		return nil, err
	}
	s.seedCacheLocked(transcriptID, records, tx)
	return tx, nil
}

// LoadHistory reads the selected chain across compaction boundaries without changing writer state.
func (s *Store) LoadHistory(ctx context.Context, id string) (*Transcript, error) {
	if err := ValidateStorageID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := s.loadRecordsLocked(s.transcriptPath(id))
	if err != nil {
		return nil, err
	}
	return project(records, false)
}

func (s *Store) List(ctx context.Context, projectID string, opts ListOptions) ([]Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if projectID != "" && s.projectID != "" && projectID != s.projectID {
		return []Summary{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := os.ReadDir(filepath.Join(s.baseDir, "transcripts"))
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.IsDir() || filepath.Ext(file.Name()) != ".jsonl" {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".jsonl")
		item, err := s.summaryLocked(id)
		if err != nil {
			log.Logger().Warn("skipping unreadable session", zap.String("session", id), zap.Error(err))
			continue
		}
		if opts.IncludeSidechain || !item.IsSidechain {
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(a, b Summary) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return items, nil
}

func (s *Store) Delete(ctx context.Context, transcriptID string) (err error) {
	release, err := s.Claim(transcriptID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(s.transcriptPath(transcriptID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete transcript file: %w", err)
	}
	delete(s.sessions, transcriptID)

	delete(s.summaries, transcriptID)
	return os.RemoveAll(s.summaryPath(transcriptID))
}

func (s *Store) transcriptPath(transcriptID string) string {
	return filepath.Join(s.baseDir, "transcripts", transcriptID+".jsonl")
}

func (s *Store) TranscriptPath(transcriptID string) string {
	return s.transcriptPath(transcriptID)
}

// appendRecord writes one record to the JSONL. fsync is gated by sync so
// hot-path telemetry (system.section.*, tools.*, inference.requested) can be
// buffered in the OS page cache and rolled up to disk at turn boundaries.
//
// Durability classes:
//   - sync=true: user input (message.appended), turn-completion
//     (inference.responded), lifecycle events (session.started, session.compacted).
//     A crash after these must not lose them.
//   - sync=false: pure telemetry. Worst case on crash: the in-flight turn's
//     telemetry is lost, but the message/state of preceding turns is intact
//     because their write fsynced.
//
// Single-process per file: the append+close pair preserves order regardless
// of fsync; durability is the only thing the flag toggles.
func (s *Store) appendRecord(path string, rec Record, sync bool) error {
	// NewStore creates transcripts/ at construction; no per-record MkdirAll.
	if err := s.repairTailLocked(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open transcript file: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		f.Close()
		return fmt.Errorf("append transcript record: %w", err)
	}
	if sync {
		if err := f.Sync(); err != nil {
			f.Close()
			return fmt.Errorf("sync transcript file: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close transcript file: %w", err)
	}
	return nil
}

// cacheLocked returns the per-session cache, creating an empty (unwarmed)
// entry on first access. Callers must hold s.mu (write).
func (s *Store) cacheLocked(transcriptID string) *sessionCache {
	if s.sessions == nil {
		s.sessions = make(map[string]*sessionCache)
	}
	c, ok := s.sessions[transcriptID]
	if !ok {
		c = &sessionCache{persistedIDs: make(map[string]struct{})}
		s.sessions[transcriptID] = c
	}
	return c
}

// warmupCachesLocked performs a single forward scan of the transcript and
// populates persistedIDs, lastGitBranch, leafMessageID, and latestState. On
// resumed sessions this replaces four independent O(N) scans (one for each
// cache) with one. Idempotent.
func (s *Store) warmupCachesLocked(id string) error {
	c := s.cacheLocked(id)
	if c.warmed {
		return nil
	}
	records, err := s.loadRecordsLocked(s.transcriptPath(id))
	if os.IsNotExist(err) {
		c.warmed = true
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := Project(records)
	if err != nil {
		return err
	}
	s.seedCacheLocked(id, records, tx)
	return nil
}
func (s *Store) seedCacheLocked(id string, records []Record, tx *Transcript) {
	c := s.cacheLocked(id)
	c.persistedIDs = make(map[string]struct{})
	for _, r := range records {
		if r.Message != nil {
			c.persistedIDs[r.Message.ID] = struct{}{}
		}
		for _, m := range r.Messages {
			c.persistedIDs[m.ID] = struct{}{}
		}
		if r.GitBranch != "" {
			c.lastGitBranch = r.GitBranch
		}
		if r.Inference != nil {
			c.inference = max(c.inference, r.Inference.Sequence)
			c.exchange = max(c.exchange, r.Inference.Exchange)
		}
	}
	c.latestState = tx.State
	c.latestState.Tasks = slices.Clone(tx.State.Tasks)
	for i := range c.latestState.Tasks {
		task := &c.latestState.Tasks[i]
		task.Metadata = maps.Clone(task.Metadata)
		task.Blocks = slices.Clone(task.Blocks)
		task.BlockedBy = slices.Clone(task.BlockedBy)
	}
	c.messageIDs = make([]string, len(tx.Messages))
	c.leafMessageID = ""
	for i, n := range tx.Messages {
		c.messageIDs[i] = n.ID
		c.leafMessageID = n.ID
	}
	c.warmed = true
}

// LastMessageID returns the active-chain leaf message ID for an existing
// transcript, or "" if the transcript doesn't exist or has no messages. Fast
// path uses a read lock when the cache is warm; cold path upgrades to a
// write lock to run warmupCachesLocked. One O(N) scan per session per
// process, then O(1).
func (s *Store) LastMessageID(ctx context.Context, transcriptID string) (string, error) {
	if err := ValidateStorageID(transcriptID); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.RLock()
	if c, ok := s.sessions[transcriptID]; ok && c.warmed {
		leaf := c.leafMessageID
		s.mu.RUnlock()
		return leaf, nil
	}
	s.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.warmupCachesLocked(transcriptID); err != nil {
		return "", err
	}
	return s.sessions[transcriptID].leafMessageID, nil
}

func (s *Store) loadRecordsLocked(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()
	var records []Record
	for rec, err := range Read(f) {
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s *Store) buildSummaryLocked(transcriptID string) (Summary, error) {
	records, err := s.loadRecordsLocked(s.transcriptPath(transcriptID))
	if err != nil {
		return Summary{}, err
	}
	transcript, err := Project(records)
	if err != nil {
		return Summary{}, err
	}

	title := transcript.State.Title
	if title == "" {
		title = firstUserText(transcript.Messages)
	}

	return Summary{
		ID:           transcriptID,
		FullPath:     s.transcriptPath(transcriptID),
		CreatedAt:    transcript.CreatedAt,
		UpdatedAt:    transcript.UpdatedAt,
		Title:        title,
		LastPrompt:   coalesce(transcript.State.LastPrompt, lastUserText(transcript.Messages)),
		MessageCount: len(transcript.Messages),
		GitBranch:    recordGitBranch(records),
		IsSidechain:  anySidechain(records),
		Provider:     transcript.Provider, Model: transcript.Model,
	}, nil
}

func firstUserText(messages []ai.Message) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if text := firstTextBlock(msg.Content); text != "" {
			return text
		}
	}
	return ""
}

func lastUserText(messages []ai.Message) string {
	for _, message := range slices.Backward(messages) {
		if message.Role != "user" {
			continue
		}
		if text := firstTextBlock(message.Content); text != "" {
			return text
		}
	}
	return ""
}

func firstTextBlock(content ai.Content) string {
	for _, block := range content {
		if block.Type == ai.BlockToolResult {
			return ""
		}
		if block.Type == ai.BlockText && block.Text != "" {
			return block.Text
		}
	}
	return ""
}

func recordGitBranch(records []Record) string {
	for _, rec := range slices.Backward(records) {
		if rec.GitBranch != "" {
			return rec.GitBranch
		}
	}
	return ""
}
func anySidechain(records []Record) bool {
	for _, rec := range records {
		if rec.IsSidechain || rec.Start != nil && rec.Start.ParentSessionID != "" {
			return true
		}
	}
	return false
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
