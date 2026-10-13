package session

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Writer turns core.Agent lifecycle events into transcript records in
// causal order — every message.appended lands before the inference.requested
// that consumes it. One Writer is bound to one (sessionID, agentID) pair;
// OnAgentEvent is the core.Config.OnEvent callback.
type Writer struct {
	store       *transcript.Store
	sessionID   string
	agentID     string
	isSidechain bool

	inference    int
	exchange     int
	exchangeBase int
	maxTokens    int
	err          error
	release      func()
	closed       bool

	mu            sync.Mutex
	lastRequest   *requestState
	lastMessageID string // for parentMessageId on message.appended
}

type requestState struct {
	sequence  int
	startedAt time.Time
}

// WriterOptions binds one writer to a session. Model settings here describe
// its initial configuration; every inference records the actual client.
type WriterOptions struct {
	Store           *transcript.Store
	SessionID       string
	AgentID         string
	Provider        string
	Model           string
	MaxTokens       int
	Cwd             string
	ParentSessionID string
	// Sidechain marks each message.appended that flows through this
	// writer as a sidechain entry — used for forked or subagent runs
	// (e.g. the L1 self-learning review) so the inspector can filter
	// them out of the main thread but still surface them on demand.
	Sidechain bool
}

// NewWriter writes session.started before returning so observer-driven
// replay (system sections, tools) lands on a file that already carries
// session metadata. Start is idempotent so Store.Save's own Start stays a
// no-op.
func NewWriter(opts WriterOptions) *Writer {
	r := &Writer{store: opts.Store, sessionID: opts.SessionID, agentID: opts.AgentID, isSidechain: opts.Sidechain, maxTokens: opts.MaxTokens}
	if opts.Store != nil && opts.SessionID != "" {
		r.release, r.err = opts.Store.Claim(opts.SessionID)
		if r.err == nil {
			r.err = opts.Store.Start(context.Background(), transcript.StartCommand{
				SessionID:       opts.SessionID,
				Cwd:             opts.Cwd,
				Provider:        opts.Provider,
				Model:           opts.Model,
				MaxTokens:       opts.MaxTokens,
				AgentID:         opts.AgentID,
				ParentSessionID: opts.ParentSessionID,
				Time:            time.Now(),
			})
		}
	}
	if r.err == nil && r.store != nil && r.sessionID != "" {
		r.lastMessageID, r.inference, r.exchange, r.err = r.store.Position(context.Background(), r.sessionID)
	}
	r.exchangeBase = r.exchange
	return r
}

// SessionID returns the session ID the Writer writes to — useful
// for surfacing the L1 fork's session in the post-review recap so a
// user can "san --resume <id>" to replay the review in isolation.
func (r *Writer) SessionID() string {
	if r == nil {
		return ""
	}
	return r.sessionID
}

// audit runs write under r's nil-guard, time-stamps it, and logs but does
// not propagate failures — audit telemetry must never block the writer's
// caller (hook engine, permission decider, skill registry).
func (r *Writer) audit(name string, write func(time.Time) error) {
	if r == nil || r.store == nil || r.sessionID == "" || r.isClosed() {
		return
	}
	if err := write(time.Now()); err != nil {
		log.Logger().Warn("writer: append "+name+" failed", zap.Error(err))
	}
}

// RecordHook writes one hook.fired record.
func (r *Writer) RecordHook(rec transcript.HookRecord) {
	r.audit("hook", func(t time.Time) error {
		return r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: t, Type: transcript.HookFired, Hook: &rec})
	})
}

func (r *Writer) RecordSkillState(rec transcript.SkillRecord) {
	r.audit("skill state", func(t time.Time) error {
		return r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: t, Type: transcript.SkillStateChanged,

			Skill: &rec,
		})
	})
}

func (r *Writer) RecordPermissionRequired(rec transcript.PermissionRecord) {
	r.recordPermission(transcript.PermissionRequired, rec)
}

// RecordPermissionDecided emits permission.decided for a terminal allow/reject.
func (r *Writer) RecordPermissionDecided(rec transcript.PermissionRecord) {
	r.recordPermission(transcript.PermissionDecided, rec)
}

func (r *Writer) recordPermission(typ string, rec transcript.PermissionRecord) {
	r.audit("permission", func(t time.Time) error {
		return r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: t, Type: typ, Permission: &rec})
	})
}

// OnAgentEvent is the core.Config.OnEvent callback. It dispatches by event
// type and writes the corresponding transcript record. Errors are logged
// rather than propagated — failing to record telemetry must not break the
// running session.
func (r *Writer) OnAgentEvent(ev core.Event) {
	if r == nil || r.store == nil || r.sessionID == "" {
		return
	}
	if _, stopped := ev.(core.AgentStopped); stopped && r.isSidechain {
		r.Close()
		return
	}
	if r.Err() != nil || r.isClosed() {
		return
	}
	switch e := ev.(type) {
	case core.AgentStarted:
		r.mu.Lock()
		r.exchangeBase = r.exchange
		r.mu.Unlock()
	case sdkagent.MessagesReplaced:
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Type: transcript.ConversationReplaced, Messages: e.Messages}); err != nil {
			r.err = err
			return
		}
		r.lastMessageID = ""
		if len(e.Messages) > 0 {
			r.lastMessageID = e.Messages[len(e.Messages)-1].ID
		}

	case sdkagent.MessageStart:
		r.onInferenceRequested(e)
	case sdkagent.MessageEnd:
		r.onInferenceEnded(e)
	case core.SystemChange:
		r.onSystemChange(e)
	case core.ToolsChange:
		r.onToolsChange(e)
	case sdkagent.ToolEnd:
		r.onToolEnd(e)
	case sdkagent.MessageAdded:
		r.onAppend(e.Message)
	case core.Compacted:
		r.onCompact(e)
	}
}

// onCompact persists the compaction boundary. The summary message itself is
// recorded via the preceding message.appended (compaction emits OnAppend for
// it first); this record marks that summary's ID as the point where replay
// stops walking parents, so the summarized-away history is not resurrected.
func (r *Writer) onCompact(info core.Compacted) {
	if info.SummaryMessageID == "" {
		return
	}
	err := r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: time.Now(), Type: transcript.SessionCompacted, Compaction: &transcript.Compaction{MessageID: info.SummaryMessageID}})
	if err != nil {
		r.fail(err)
		log.Logger().Warn("writer: compact boundary failed", zap.Error(err))
	}
}

// onAppend persists message.appended at the moment the message enters the
// chain. This is what guarantees "causes before consumers": any subsequent
// inference.requested lands after the messages it references.
func (r *Writer) onAppend(msg core.Message) {
	if msg.ID == "" {
		return
	}

	content := msg.Content
	if len(content) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil || r.closed {
		return
	}
	parent := r.lastMessageID
	err := r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: time.Now(), ParentMessageID: parent, AgentID: r.agentID, IsSidechain: r.isSidechain, Type: transcript.MessageAppended, Message: &msg})
	if err != nil {
		r.err = err
		log.Logger().Warn("writer: append message failed", zap.Error(err))
		return
	}
	r.lastMessageID = msg.ID
}

func (r *Writer) onToolsChange(c core.ToolsChange) {
	typ := transcript.ToolAdded
	payload := transcript.ToolRecord{Caller: c.Caller}
	if c.Removed {
		typ = transcript.ToolRemoved
		payload.Name = c.Name
	} else {
		payload.Schema = &c.Schema
	}
	err := r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: time.Now(), Type: typ, Tool: &payload})
	if err != nil {
		log.Logger().Warn("writer: append tools change failed", zap.Error(err))
	}
}

func (r *Writer) onSystemChange(c core.SystemChange) {
	typ := transcript.SystemSectionAdded
	if c.Removed {
		typ = transcript.SystemSectionRemoved
	}
	err := r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: time.Now(), Type: typ, System: &transcript.SystemSectionRecord{
		Name:    c.Name,
		Slot:    c.Slot,
		Content: c.Content,
		Caller:  c.Caller,
	},
	})
	if err != nil {
		log.Logger().Warn("writer: append system section failed", zap.Error(err))
	}
}

// onInferenceRequested records the call as digests rather than content, so the
// transcript can reference what went out without copying it on every turn. The
// digesting lives here because this is the only thing that ever wanted it.
func (r *Writer) onInferenceRequested(e sdkagent.MessageStart) {
	if e.Inference == nil {
		return
	}
	ic := inferenceDigest(e.Inference)
	request := ai.Request{MaxTokens: r.maxTokens}
	for _, option := range e.Inference.Options {
		option(&request)
	}
	ic.MaxTokens = request.MaxTokens
	if e.Inference.Client != nil {
		ic.Provider = e.Inference.Client.Driver().Name()
		ic.Model = e.Inference.Client.Model().ID
	}

	r.mu.Lock()
	r.inference++
	inference := r.inference
	exchange := r.exchangeBase + e.Turn
	r.exchange = max(r.exchange, exchange)
	r.mu.Unlock()
	now := time.Now()

	r.mu.Lock()
	r.lastRequest = &requestState{
		sequence:  inference,
		startedAt: now,
	}
	r.mu.Unlock()

	r.appendInference(now, transcript.InferenceRequested, transcript.InferenceRecord{
		Sequence: inference,
		Exchange: exchange,
		Attempt:  e.Attempt,
		Provider: ic.Provider, Model: ic.Model, MaxTokens: ic.MaxTokens,
		SystemDigest: ic.SystemDigest,
		ToolsDigest:  ic.ToolsDigest,
		MessageIDs:   ic.MessageIDs,
	})
}

// onInferenceEnded closes out the open request. A call that failed ends the
// span just as a response does, and says why: without inference.failed the
// attempt leaves an inference.requested no record ever answers.
func (r *Writer) onInferenceEnded(e sdkagent.MessageEnd) {
	now := time.Now()

	if e.Err != nil {
		sequence, latencyMs := r.closePendingRequest(now)
		r.appendInference(now, transcript.InferenceFailed, transcript.InferenceRecord{
			Sequence:  sequence,
			Exchange:  r.exchangeBase + e.Turn,
			LatencyMs: latencyMs,
			Error:     e.Err.Error(),
			Attempt:   e.Attempt,
			Retryable: ai.IsRetryable(e.Err),
		})
		return
	}

	resp := e.Response
	if resp == nil {
		return
	}
	sequence, latencyMs := r.closePendingRequest(now)

	r.appendInference(now, transcript.InferenceResponded, transcript.InferenceRecord{
		Sequence: sequence,
		Model:    resp.Model, Exchange: r.exchangeBase + e.Turn, Attempt: e.Attempt,
		StopReason: string(resp.StopReason),
		LatencyMs:  latencyMs,
		// The transcript's own names on the left: this is a disk format
		// and keeps the keys it was written with.
		Usage: &transcript.InferenceUsage{
			InputTokens:              resp.Usage.Input,
			OutputTokens:             resp.Usage.Output,
			CacheCreationInputTokens: resp.Usage.CacheWrite,
			CacheReadInputTokens:     resp.Usage.CacheRead,
		},
	})
}

// closePendingRequest consumes the open inference.requested and returns its
// sequence plus elapsed time. Zero sequence when no request is pending.
func (r *Writer) closePendingRequest(now time.Time) (sequence int, latencyMs int64) {
	r.mu.Lock()
	prev := r.lastRequest
	r.lastRequest = nil
	r.mu.Unlock()

	if prev == nil {
		return 0, 0
	}
	return prev.sequence, now.Sub(prev.startedAt).Milliseconds()
}

// appendInference writes one inference record, logging rather than
// propagating a failure: losing telemetry must not break the session.
func (r *Writer) appendInference(now time.Time, typ string, rec transcript.InferenceRecord) {
	err := r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: now, Type: typ, Inference: &rec})
	if err != nil {
		r.fail(err)
		log.Logger().Warn("writer: append "+typ+" failed", zap.Error(err))
	}
}

// inferenceDigest is what the transcript keeps of one call: content-addressed,
// so a record can reference the system prompt and the toolset without carrying
// either. It moved here from core when the loop's events stopped being
// translated — nothing else ever read it.
func inferenceDigest(inf *sdkagent.Inference) transcript.InferenceRecord {
	schemas := make([]core.ToolSchema, 0, len(inf.Tools))
	for _, t := range inf.Tools {
		schemas = append(schemas, t.Schema)
	}
	return transcript.InferenceRecord{
		SystemDigest: transcript.DigestSystem(inf.System),
		ToolsDigest:  transcript.DigestTools(schemas),
		MessageIDs:   messageIDs(inf.Messages),
	}
}

// Err reports the persistence failure that stopped recording.
func (r *Writer) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *Writer) fail(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *Writer) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	release := r.release
	r.mu.Unlock()
	if release != nil {
		release()
	}
}

func (r *Writer) isClosed() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed }

// RecordNotice persists display-only text at its position in the conversation.
func (r *Writer) RecordNotice(text string, agent bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	after := r.lastMessageID
	r.mu.Unlock()
	r.audit("notice", func(t time.Time) error {
		return r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: t, Type: transcript.NoticeAdded, Notice: &transcript.NoticeRecord{AfterMessageID: after, Text: text, Agent: agent}})
	})
}

func (r *Writer) onToolEnd(e sdkagent.ToolEnd) {
	details := transcript.ToolDetailsRecord{ToolCallID: e.ID}
	switch v := e.Result.Details.(type) {
	case toolresult.FileChangeDetails:
		details.FileChange = &v
	case toolresult.BashDetails:
		details.Bash = &v
	case toolresult.AgentDetails:
		details.Agent = &v
	default:
		return
	}
	r.audit("tool details", func(t time.Time) error {
		return r.store.Append(context.Background(), transcript.Record{SessionID: r.sessionID, Time: t, Type: transcript.ToolCompleted, ToolDetails: &details})
	})
}
