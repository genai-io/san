package session

import (
	"context"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// RecordEvent adapts native agent events into the same Append path as
// application records. The runtime captures this opened session, never a mutable ID.
func (r *Session) RecordEvent(ev core.Event) {
	if r == nil || r.store == nil || r.sessionID == "" {
		return
	}
	if _, stopped := ev.(core.AgentStopped); stopped && r.isSidechain {
		_ = r.Close()
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil || r.closed {
		return
	}
	switch e := ev.(type) {
	case core.AgentStarted:
		r.runExchangeOffset = r.exchangeCount
	case sdkagent.MessagesReplaced:
		_ = r.appendLocked(context.Background(), transcript.Record{Type: transcript.ConversationReplaced, Messages: e.Messages})

	case sdkagent.MessageStart:
		r.recordInferenceStarted(e)
	case sdkagent.MessageEnd:
		r.recordInferenceEnded(e)
	case core.SystemChange:
		r.recordSystemChange(e)
	case core.ToolsChange:
		r.recordToolChange(e)
	case sdkagent.ToolEnd:
		r.recordToolDetails(e)
	case sdkagent.MessageAdded:
		if len(e.Message.Content) == 0 {
			return
		}
		msg := e.Message
		_ = r.appendLocked(context.Background(), transcript.Record{Type: transcript.MessageAppended, Message: &msg})
	case core.Compacted:
		r.recordCompaction(e)
	}
}

// recordCompaction persists the compaction boundary. The summary message itself is
// recorded via the preceding message.appended (compaction emits OnAppend for
// it first); this record marks that summary's ID as the point where replay
// stops walking parents, so the summarized-away history is not resurrected.
func (r *Session) recordCompaction(info core.Compacted) {
	if info.SummaryMessageID == "" {
		return
	}
	_ = r.appendLocked(context.Background(), transcript.Record{SessionID: r.sessionID, Time: time.Now(), Type: transcript.SessionCompacted, Compaction: &transcript.Compaction{MessageID: info.SummaryMessageID}})
}

func (r *Session) recordToolChange(c core.ToolsChange) {
	typ := transcript.ToolAdded
	payload := transcript.ToolRecord{Caller: c.Caller}
	if c.Removed {
		typ = transcript.ToolRemoved
		payload.Name = c.Name
	} else {
		payload.Schema = &c.Schema
	}
	_ = r.appendLocked(context.Background(), transcript.Record{SessionID: r.sessionID, Time: time.Now(), Type: typ, Tool: &payload})
}

func (r *Session) recordSystemChange(c core.SystemChange) {
	typ := transcript.SystemSectionAdded
	if c.Removed {
		typ = transcript.SystemSectionRemoved
	}
	_ = r.appendLocked(context.Background(), transcript.Record{Time: time.Now(), Type: typ, System: &transcript.SystemSectionRecord{
		Name:    c.Name,
		Slot:    c.Slot,
		Content: c.Content,
		Caller:  c.Caller,
	},
	})
}

// recordInferenceStarted records the call as digests rather than content, so the
// transcript can reference what went out without copying it on every turn. The
// digesting lives here because this is the only thing that ever wanted it.
func (r *Session) recordInferenceStarted(e sdkagent.MessageStart) {
	if e.Inference == nil {
		return
	}
	ic := inferenceDigest(e.Inference)
	request := ai.Request{MaxTokens: r.maxOutputTokens}
	for _, option := range e.Inference.Options {
		option(&request)
	}
	ic.MaxTokens = request.MaxTokens
	if e.Inference.Client != nil {
		ic.Provider = e.Inference.Client.Driver().Name()
		ic.Model = e.Inference.Client.Model().ID
	}

	r.inferenceSequence++
	inference := r.inferenceSequence
	exchange := r.runExchangeOffset + e.Turn
	r.exchangeCount = max(r.exchangeCount, exchange)
	now := time.Now()

	r.pendingRequest = &pendingInference{
		sequence:  inference,
		startedAt: now,
	}

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

// recordInferenceEnded closes out the open request. A call that failed ends the
// span just as a response does, and says why: without inference.failed the
// attempt leaves an inference.requested no record ever answers.
func (r *Session) recordInferenceEnded(e sdkagent.MessageEnd) {
	now := time.Now()

	if e.Err != nil {
		sequence, latencyMs := r.closePendingRequest(now)
		r.appendInference(now, transcript.InferenceFailed, transcript.InferenceRecord{
			Sequence:  sequence,
			Exchange:  r.runExchangeOffset + e.Turn,
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
		Model:    resp.Model, Exchange: r.runExchangeOffset + e.Turn, Attempt: e.Attempt,
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
func (r *Session) closePendingRequest(now time.Time) (sequence int, latencyMs int64) {
	prev := r.pendingRequest
	r.pendingRequest = nil

	if prev == nil {
		return 0, 0
	}
	return prev.sequence, now.Sub(prev.startedAt).Milliseconds()
}

// appendInference uses the shared append implementation and retains failures in Err.
func (r *Session) appendInference(now time.Time, typ string, rec transcript.InferenceRecord) {
	branch := ""
	if typ == transcript.InferenceRequested {
		branch = getGitBranch(r.cwd)
	}
	_ = r.appendLocked(context.Background(), transcript.Record{Time: now, GitBranch: branch, Type: typ, Inference: &rec})
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

func (r *Session) recordToolDetails(e sdkagent.ToolEnd) {
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
	_ = r.appendLocked(context.Background(), transcript.Record{Type: transcript.ToolCompleted, ToolDetails: &details})
}
