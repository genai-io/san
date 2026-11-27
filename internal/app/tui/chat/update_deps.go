package chat

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/runtime"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// UpdateDeps supplies the callbacks and services Update needs from the root model.
//
// The prefix says where a member comes from:
//
//   - On*     — the agent did something. One per agent event, and they arrive
//     in the outbox's order, so they have a fixed place on the turn's timeline.
//   - Handle* — a system message arrived: a background command's result, a
//     timer, a side channel. No place on that timeline; it can land any time.
//   - a verb  — not a callback at all, but a service Update calls on the root model.
type UpdateDeps interface {
	// ── On*: agent lifecycle ────────────────────────────────────
	OnInference(resp *ai.Response) // PostInfer
	// OnToolResult hands back the result message the model is told; the
	// structured form the interface draws arrives separately, on ToolEnd.
	OnToolResult(tr core.ToolResult) *core.ToolResult
	// OnStepEnd fires when a tool batch completes and the turn continues, so
	// pending work (one queued user message) can reach the
	// agent in time for its next step.
	OnStepEnd() tea.Cmd
	OnTurnEnd(result core.Result) tea.Cmd
	OnAgentStop(err error) tea.Cmd
	OnCompactStart(count int) tea.Cmd
	OnCompactEnd(end sdkagent.CompactionEnd) tea.Cmd
	OnCompacted(info core.Compacted) tea.Cmd

	// ── Handle*: system messages ────────────────────────────────
	HandlePermGate(req *runtime.PermGateRequest) tea.Cmd
	HandleCompactResult(msg CompactResultMsg) tea.Cmd

	// ── Services provided by the root model ─────────────────────────
	CommitMessages() []tea.Cmd
	FlushStreamingBlocks() []tea.Cmd
	// TakeJudgeDecision consumes the judge decision stashed for a tool
	// call (nil if it was not judged), to stamp onto its rendered result.
	TakeJudgeDecision(callID string) *core.JudgeDecision
	HasRunningTasks() bool
	// AgentOutbox is the selected runtime's outbox, retained through its final events.
	AgentOutbox() <-chan core.Event

	// ── Transport lifetime ──────────────────────────────────────
	// ListenForAgentEvents waits for the next batch of agent events.
	// Update re-arms after ordinary events; OnTurnEnd and OnCompacted re-arm
	// at their boundaries. OnAgentStop leaves the listener stopped.
	ListenForAgentEvents() tea.Cmd
}
