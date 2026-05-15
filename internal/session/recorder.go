package session

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/session/transcript"
)

// Recorder turns core.Agent lifecycle events into transcript records.
//
// One Recorder is bound to one (sessionID, agentID) pair. The OnAgentEvent
// method is meant to be passed as core.Config.OnEvent — it's called
// synchronously from the agent goroutine, so handlers stay fast.
//
// The recorder is additive: existing code paths (Store.Save) keep persisting
// messages and state. The recorder writes only event-driven records
// (inference.requested / inference.responded) that the snapshot path can't
// observe.
type Recorder struct {
	fs        *transcript.FileStore
	sessionID string
	agentID   string
	provider  string
	model     string
	maxTokens int

	turn atomic.Int64

	mu          sync.Mutex
	lastRequest *requestState
}

type requestState struct {
	turn       int
	startedAt  time.Time
	messageIDs []string
}

// RecorderOptions configures a Recorder. provider/model/maxTokens are captured
// at construction time and stamped on every inference record. Mid-session
// model swaps will be handled by a future model.changed event, not by mutating
// the recorder.
type RecorderOptions struct {
	FileStore *transcript.FileStore
	SessionID string
	AgentID   string
	Provider  string
	Model     string
	MaxTokens int
}

func NewRecorder(opts RecorderOptions) *Recorder {
	return &Recorder{
		fs:        opts.FileStore,
		sessionID: opts.SessionID,
		agentID:   opts.AgentID,
		provider:  opts.Provider,
		model:     opts.Model,
		maxTokens: opts.MaxTokens,
	}
}

// OnAgentEvent is the core.Config.OnEvent callback. It dispatches by event
// type and writes the corresponding transcript record. Errors are logged
// rather than propagated — failing to record telemetry must not break the
// running session.
func (r *Recorder) OnAgentEvent(ev core.Event) {
	if r == nil || r.fs == nil || r.sessionID == "" {
		return
	}
	switch ev.Type {
	case core.PreInfer:
		r.onPreInfer(ev)
	case core.PostInfer:
		r.onPostInfer(ev)
	case core.OnSystemChange:
		r.onSystemChange(ev)
	case core.OnToolsChange:
		r.onToolsChange(ev)
	}
}

func (r *Recorder) onToolsChange(ev core.Event) {
	c, ok := ev.ToolsChange()
	if !ok {
		return
	}
	typ := transcript.ToolsAdded
	payload := transcript.ToolsRecord{Caller: c.Caller}
	if c.Removed {
		typ = transcript.ToolsRemoved
		payload.Name = c.Name
	} else {
		payload.Schema = toolSchemaView(c.Schema)
	}
	err := r.fs.AppendTools(context.Background(), transcript.AppendToolsCommand{
		SessionID: r.sessionID,
		AgentID:   r.agentID,
		Time:      time.Now(),
		Type:      typ,
		Record:    payload,
	})
	if err != nil {
		log.Logger().Warn("recorder: append tools change failed", zap.Error(err))
	}
}

func toolSchemaView(s core.ToolSchema) *transcript.ToolSchemaView {
	view := &transcript.ToolSchemaView{
		Name:        s.Name,
		Description: s.Description,
	}
	if s.Parameters != nil {
		if data, err := json.Marshal(s.Parameters); err == nil {
			view.Parameters = data
		}
	}
	return view
}

func (r *Recorder) onSystemChange(ev core.Event) {
	c, ok := ev.SystemChange()
	if !ok {
		return
	}
	typ := transcript.SystemSectionAdded
	if c.Removed {
		typ = transcript.SystemSectionRemoved
	}
	err := r.fs.AppendSystemSection(context.Background(), transcript.AppendSystemSectionCommand{
		SessionID: r.sessionID,
		AgentID:   r.agentID,
		Time:      time.Now(),
		Type:      typ,
		Record: transcript.SystemSectionRecord{
			Name:    c.Name,
			Slot:    c.Slot,
			Content: c.Content,
			Caller:  c.Caller,
		},
	})
	if err != nil {
		log.Logger().Warn("recorder: append system section failed", zap.Error(err))
	}
}

func (r *Recorder) onPreInfer(ev core.Event) {
	ic, ok := ev.InferenceContext()
	if !ok {
		return
	}

	turn := int(r.turn.Add(1))
	now := time.Now()

	r.mu.Lock()
	r.lastRequest = &requestState{
		turn:       turn,
		startedAt:  now,
		messageIDs: ic.MessageIDs,
	}
	r.mu.Unlock()

	err := r.fs.AppendInference(context.Background(), transcript.AppendInferenceCommand{
		SessionID: r.sessionID,
		AgentID:   r.agentID,
		Time:      now,
		Type:      transcript.InferenceRequested,
		Record: transcript.InferenceRecord{
			Turn:         turn,
			Provider:     r.provider,
			Model:        r.model,
			MaxTokens:    r.maxTokens,
			SystemDigest: ic.SystemDigest,
			ToolsDigest:  ic.ToolsDigest,
			MessageIDs:   ic.MessageIDs,
		},
	})
	if err != nil {
		log.Logger().Warn("recorder: append inference.requested failed", zap.Error(err))
	}
}

func (r *Recorder) onPostInfer(ev core.Event) {
	resp, ok := ev.Response()
	if !ok || resp == nil {
		return
	}

	r.mu.Lock()
	prev := r.lastRequest
	r.lastRequest = nil
	r.mu.Unlock()

	now := time.Now()
	var turn int
	var latencyMs int64
	if prev != nil {
		turn = prev.turn
		latencyMs = now.Sub(prev.startedAt).Milliseconds()
	}

	err := r.fs.AppendInference(context.Background(), transcript.AppendInferenceCommand{
		SessionID: r.sessionID,
		AgentID:   r.agentID,
		Time:      now,
		Type:      transcript.InferenceResponded,
		Record: transcript.InferenceRecord{
			Turn:       turn,
			Provider:   r.provider,
			Model:      r.model,
			StopReason: string(resp.StopReason),
			LatencyMs:  latencyMs,
			Usage: &transcript.InferenceUsage{
				InputTokens:       resp.TokensIn,
				OutputTokens:      resp.TokensOut,
				CacheCreateTokens: resp.CacheCreateTokens,
				CacheReadTokens:   resp.CacheReadTokens,
			},
		},
	})
	if err != nil {
		log.Logger().Warn("recorder: append inference.responded failed", zap.Error(err))
	}
}
