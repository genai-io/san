package transcript

import (
	"context"
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
	"github.com/genai-io/sdk-go/pkg/session"
	"go.uber.org/zap"
)

// Store keeps san's transcripts in a pkg/session store: messages as its
// conversation, every other record as an application record whose data is
// the Record itself.
type Store struct {
	mu        sync.RWMutex
	baseDir   string
	projectID string
	log       *session.Store

	// sessions holds per-transcript caches keyed by session ID. Populated
	// lazily on first access via warmupCachesLocked, which does one forward
	// scan and fills every field together so resumed sessions pay one O(N)
	// scan per writer lease. Claim and Delete evict all derived state
	// at once.
	sessions map[string]*sessionCache

	writers   map[string]*session.Writer
	summaries map[string]summaryCache
}

// sessionCache holds the derived state we keep in memory to avoid re-scanning
// the transcript on every write. All fields are mu-guarded.
type sessionCache struct {
	warmed        bool                // forward scan completed
	persistedIDs  map[string]struct{} // committed message IDs
	lastGitBranch string              // sparse-emit base
	inference     int
	exchange      int
	messages      []ai.Message // the conversation a resume reads
	latestState   State        // projected state (cold-Save diff base)
}

func NewStore(baseDir, projectID string) (*Store, error) {
	sessions, err := session.Open(filepath.Join(baseDir, "transcripts"))
	if err != nil {
		return nil, fmt.Errorf("create transcripts dir: %w", err)
	}
	return &Store{baseDir: baseDir, projectID: projectID, log: sessions}, nil
}

// Start writes a new session's first record.
func (s *Store) Start(ctx context.Context, cmd StartCommand) error {
	if err := ValidateStorageID(cmd.SessionID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Stat(s.transcriptPath(cmd.SessionID)); err == nil && info.Size() > 0 {
		return nil // already started
	}
	w, done, err := s.writerLocked(cmd.SessionID)
	if err != nil {
		return err
	}
	_, err = writeRecord(w, nil, Record{
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
			Sidechain:       cmd.Sidechain,
		},
	})
	return errors.Join(err, done())
}

// writerLocked is the claimed session's writer, or, outside a claim, one
// taken for a single write; done gives that one back.
func (s *Store) writerLocked(id string) (w *session.Writer, done func() error, err error) {
	if w := s.writers[id]; w != nil {
		return w, func() error { return nil }, nil
	}
	w, err = s.log.Writer(context.Background(), id)
	if err != nil {
		return nil, nil, err
	}
	return w, w.Close, nil
}

// writeRecord writes rec onto conversation conv and returns the conversation
// it leaves. Messages are the log's conversation; every other record is an
// application record carrying itself.
func writeRecord(w *session.Writer, conv []ai.Message, rec Record) ([]ai.Message, error) {
	switch rec.Type {
	case MessageAppended:
		conv = append(slices.Clip(conv), *rec.Message)
		return conv, w.Save(conv)
	case ConversationReplaced:
		return rec.Messages, w.Save(rec.Messages)
	}
	return conv, w.Append(rec.Type, rec)
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
	case ConversationReplaced:
		if err := validateMessages(rec.Messages); err != nil {
			return err
		}
		// Message IDs name immutable content; replaying the same chain is a no-op.
		if sameMessageIDs(c.messages, rec.Messages) {
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
	w, done, err := s.writerLocked(rec.SessionID)
	if err != nil {
		return err
	}
	defer func() { _ = done() }()
	messages, err := writeRecord(w, c.messages, rec)
	if err != nil {
		return err
	}
	if rec.Type == SessionCompacted {
		// The summary named by the boundary is where the conversation now
		// starts; the log says so with a reset.
		if i := slices.IndexFunc(messages, func(m ai.Message) bool { return m.ID == rec.Compaction.MessageID }); i > 0 {
			messages = messages[i:]
			if err := w.Save(messages); err != nil {
				return err
			}
		}
	}
	c.messages = messages
	if rec.GitBranch != "" {
		c.lastGitBranch = rec.GitBranch
	}
	if rec.Message != nil {
		c.persistedIDs[rec.Message.ID] = struct{}{}
	}
	for _, m := range rec.Messages {
		c.persistedIDs[m.ID] = struct{}{}
	}
	c.latestState = nextState
	if rec.Inference != nil {
		c.inference = max(c.inference, rec.Inference.Sequence)
		c.exchange = max(c.exchange, rec.Inference.Exchange)
	}
	return nil
}

// Fork writes the source's records into a new session, with its attachment
// paths rewritten to the new session's.
//
// TODO(sdk-go): use session.Store.Fork once attachment paths no longer name
// the session; a byte copy cannot rewrite them.
func (s *Store) Fork(ctx context.Context, cmd ForkCommand) (err error) {
	if err := ValidateStorageID(cmd.SourceSessionID); err != nil {
		return err
	}
	if err := ValidateStorageID(cmd.NewSessionID); err != nil {
		return err
	}
	if exists, err := fileExists(s.transcriptPath(cmd.NewSessionID)); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("fork destination %s already exists", cmd.NewSessionID)
	}
	records, err := s.loadRecords(ctx, cmd.SourceSessionID)
	if err != nil {
		return err
	}
	release, err := s.Claim(cmd.NewSessionID)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, release())
		if err != nil {
			_ = s.log.Delete(context.Background(), cmd.NewSessionID)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.writers[cmd.NewSessionID]
	var conv []ai.Message
	for _, rec := range records {
		if rec.Message != nil {
			msg := *rec.Message
			msg.Content = msg.Content.Clone()
			s.rewriteAttachments(msg.Content, cmd.SourceSessionID, cmd.NewSessionID)
			rec.Message = &msg
		}
		rec.SessionID = cmd.NewSessionID
		if conv, err = writeRecord(w, conv, rec); err != nil {
			return fmt.Errorf("write fork record: %w", err)
		}
	}
	_, err = writeRecord(w, conv, Record{
		SessionID: cmd.NewSessionID,
		Time:      cmd.Time,
		Type:      SessionForked,
		Fork:      &SessionFork{ParentSessionID: cmd.SourceSessionID},
	})
	return err
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

func (s *Store) Load(ctx context.Context, transcriptID string) (*Transcript, error) {
	if err := ValidateStorageID(transcriptID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	records, err := s.loadRecords(ctx, transcriptID)
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
	records, err := s.loadRecords(ctx, id)
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
	infos, err := s.log.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(infos))
	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := info.ID
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

func (s *Store) Delete(ctx context.Context, transcriptID string) error {
	if err := ValidateStorageID(transcriptID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writers[transcriptID] != nil {
		return fmt.Errorf("session %s already has a writer", transcriptID)
	}
	if err := s.log.Delete(ctx, transcriptID); err != nil && !errors.Is(err, session.ErrNotFound) {
		return fmt.Errorf("delete transcript: %w", err)
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
	records, err := s.loadRecords(context.Background(), id)
	if errors.Is(err, session.ErrNotFound) {
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
	c.messages = tx.Messages
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
		leaf := lastID(c.messages)
		s.mu.RUnlock()
		return leaf, nil
	}
	s.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.warmupCachesLocked(transcriptID); err != nil {
		return "", err
	}
	return lastID(s.sessions[transcriptID].messages), nil
}

func lastID(messages []ai.Message) string {
	if len(messages) == 0 {
		return ""
	}
	return messages[len(messages)-1].ID
}

func (s *Store) loadRecords(ctx context.Context, id string) ([]Record, error) {
	var records []Record
	for rec, err := range s.Records(ctx, id) {
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s *Store) buildSummaryLocked(transcriptID string) (Summary, error) {
	records, err := s.loadRecords(context.Background(), transcriptID)
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
		if rec.Start != nil && (rec.Start.Sidechain || rec.Start.ParentSessionID != "") {
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
