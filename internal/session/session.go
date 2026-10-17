package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/transcript"
	"go.uber.org/zap"
)

var ErrClosed = errors.New("session is closed")

// Session is one opened durable identity. It owns the writer lease until Close.
type Session struct {
	mu                sync.Mutex
	store             *transcript.Store
	sessionID         string
	agentID           string
	cwd               string
	isSidechain       bool
	inferenceSequence int
	exchangeCount     int
	runExchangeOffset int
	maxOutputTokens   int
	pendingRequest    *pendingInference
	err               error
	release           func() error
	closed            bool
}

type pendingInference struct {
	sequence  int
	startedAt time.Time
}

// CreateOptions describes a new session; ID is assigned when omitted.
type CreateOptions struct {
	ID              string
	AgentID         string
	Provider        string
	Model           string
	MaxTokens       int
	Cwd             string
	CreatedAt       time.Time
	ParentSessionID string
	Sidechain       bool
}

func newSession(store *transcript.Store, opts CreateOptions) (*Session, error) {
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = time.Now()
	}
	release, err := store.Claim(opts.ID)
	if err != nil {
		return nil, err
	}
	if err := store.Start(context.Background(), transcript.StartCommand{
		SessionID: opts.ID, AgentID: opts.AgentID, Provider: opts.Provider,
		Model: opts.Model, MaxTokens: opts.MaxTokens, Cwd: opts.Cwd,
		ParentSessionID: opts.ParentSessionID, Sidechain: opts.Sidechain, Time: opts.CreatedAt,
	}); err != nil {
		return nil, errors.Join(err, release())
	}
	return sessionFromLease(store, opts, release)
}

func sessionFromLease(store *transcript.Store, opts CreateOptions, release func() error) (*Session, error) {
	_, inference, exchange, err := store.Position(context.Background(), opts.ID)
	if err != nil {
		return nil, errors.Join(err, release())
	}
	return &Session{store: store, sessionID: opts.ID, agentID: opts.AgentID, cwd: opts.Cwd,
		isSidechain: opts.Sidechain, maxOutputTokens: opts.MaxTokens, release: release,
		inferenceSequence: inference, exchangeCount: exchange, runExchangeOffset: exchange}, nil
}

func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.sessionID
}
func (s *Session) TranscriptPath() string {
	if s == nil || s.store == nil {
		return ""
	}
	return s.store.TranscriptPath(s.sessionID)
}

// Append is the only persistence entrance for messages, state, and audit records.
// The session supplies identity and ordering; the transcript validates payloads.
func (s *Session) Append(ctx context.Context, rec transcript.Record) error {
	if s == nil {
		return ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(ctx, rec)
}

func (s *Session) appendLocked(ctx context.Context, rec transcript.Record) error {
	if s.closed {
		return ErrClosed
	}
	if s.err != nil {
		return s.err
	}
	if rec.SessionID != "" && rec.SessionID != s.sessionID {
		return fmt.Errorf("record belongs to session %s, want %s", rec.SessionID, s.sessionID)
	}
	rec.SessionID = s.sessionID
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	if rec.Type == transcript.NoticeAdded && rec.Notice != nil {
		notice := *rec.Notice
		if notice.AfterMessageID == "" {
			var err error
			notice.AfterMessageID, err = s.store.LastMessageID(ctx, s.sessionID)
			if err != nil {
				s.err = err
				return err
			}
		}
		rec.Notice = &notice
	}
	if err := s.store.Append(ctx, rec); err != nil {
		switch rec.Type {
		case transcript.MessageAppended, transcript.ConversationReplaced, transcript.SessionCompacted,
			transcript.SessionStatePatched, transcript.InferenceRequested, transcript.InferenceResponded, transcript.InferenceFailed:
			s.err = err
		}
		log.Logger().Warn("session: append "+rec.Type+" failed", zap.Error(err))
		return err
	}
	if rec.Inference != nil {
		s.inferenceSequence = max(s.inferenceSequence, rec.Inference.Sequence)
		s.exchangeCount = max(s.exchangeCount, rec.Inference.Exchange)
	}
	return nil
}

// Err reports the persistence failure, or ErrClosed after resource retirement.
func (s *Session) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return ErrClosed
	}
	return nil
}

// Close releases this session's writer lease after any in-flight append.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.release != nil {
		return s.release()
	}
	return nil
}
