package subagent

import (
	"context"
	"fmt"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool"
	"go.uber.org/zap"
)

type preparedRun struct {
	req       tool.AgentExecRequest
	cfg       *runConfig
	cwd       string
	startedAt time.Time
	hookID    string
	// trace is the tool-call trail included in the parent-visible result.
	// Telemetry lines (model, mode, token usage, spinner text) go only to
	// OnActivity for the UI — they would be noise in the parent's context.
	trace            []string
	inputTokens      int
	outputTokens     int
	finishWorkspace  func() (keptPath string)
	keptWorktreePath string
	workspaceDone    bool
}

// close releases the workspace if no explicit settleWorkspace ran (error
// paths); settled runs are a no-op.
func (r *preparedRun) close() {
	if r != nil {
		r.settleWorkspace()
	}
}

// settleWorkspace finalizes worktree isolation once: a clean worktree is
// removed, one with uncommitted changes is preserved and remembered so the
// result can point the parent at it.
func (r *preparedRun) settleWorkspace() {
	if r.workspaceDone || r.finishWorkspace == nil {
		return
	}
	r.workspaceDone = true
	r.keptWorktreePath = r.finishWorkspace()
}

// sendTrace records a tool-call line in the result trace and streams it to
// the UI.
func (r *preparedRun) sendTrace(msg string) {
	r.trace = append(r.trace, msg)
	r.sendTelemetry(msg)
}

// sendTelemetry streams a UI-only activity line (model, mode, usage).
func (r *preparedRun) sendTelemetry(msg string) {
	if r.req.OnActivity != nil {
		r.req.OnActivity(msg)
	}
}

func (r *preparedRun) recordUsage(resp *core.InferResponse) {
	if r.req.OnActivity == nil || resp == nil {
		return
	}
	r.inputTokens += resp.InputTokens
	r.outputTokens += resp.OutputTokens
	if r.inputTokens > 0 || r.outputTokens > 0 {
		r.sendTelemetry(formatUsageActivity(r.inputTokens, r.outputTokens))
	}
}

func (e *Executor) prepareRun(ctx context.Context, req tool.AgentExecRequest) (*preparedRun, error) {
	if err := e.validateRequest(req); err != nil {
		return nil, err
	}

	cfg, err := e.prepareRunConfig(ctx, req)
	if err != nil {
		return nil, err
	}

	agentCwd, finishWorkspace, err := e.prepareWorkspace(req, cfg.config)
	if err != nil {
		return nil, err
	}

	return &preparedRun{
		req:             req,
		cfg:             cfg,
		cwd:             agentCwd,
		startedAt:       time.Now(),
		hookID:          "a" + generateShortID(),
		trace:           make([]string, 0, 16),
		finishWorkspace: finishWorkspace,
	}, nil
}

func (e *Executor) attachRunContext(ctx context.Context, displayName string) context.Context {
	tracker := log.NewAgentTurnTracker(displayName, nil)
	return log.WithAgentTracker(ctx, tracker)
}

func (e *Executor) logRunStart(run *preparedRun) {
	log.Logger().Info("Starting agent execution",
		zap.String("agent", run.cfg.displayName),
		zap.String("description", run.req.Description),
		zap.Int("maxSteps", run.cfg.maxSteps),
	)
}

func (e *Executor) executePreparedRun(ctx context.Context, run *preparedRun) (*core.Result, error) {
	var onToolExec func(string, map[string]any)
	if run.req.OnActivity != nil {
		run.sendTelemetry(fmt.Sprintf("Model: %s", run.cfg.modelID))
		run.sendTelemetry(fmt.Sprintf("Mode: %s · max %d steps", displayPermissionMode(run.cfg.permMode), run.cfg.maxSteps))
		onToolExec = func(name string, params map[string]any) {
			run.sendTrace(formatToolActivity(name, params))
		}
	}
	ag, cleanupAgent, err := e.buildAgent(ctx, run, onToolExec, func(ev core.Event) {
		if resp, ok := ev.Response(); ok && ev.Type == core.PostInfer {
			run.recordUsage(resp)
		}
	})
	if err != nil {
		return nil, err
	}
	defer cleanupAgent()

	if err := e.loadConversation(ag, ctx, run.cfg, run.req); err != nil {
		return nil, err
	}
	if run.req.OnActivity != nil {
		run.sendTelemetry("Thinking...")
	}

	// A background subagent registers its task id with the broker for the
	// length of the run, so main can message it mid-flight. A routed message
	// lands in the subagent's inbox and is read at its next step boundary.
	if run.req.TaskID != "" {
		ctx = tool.WithAgentID(ctx, run.req.TaskID)
		broker.Register(run.req.TaskID, func(m broker.Message) {
			select {
			case ag.Inbox() <- core.UserMessage(m.Content, nil):
			default:
				log.Logger().Warn("subagent inbox full; dropped message",
					zap.String("from", m.From))
			}
		})
		defer broker.Unregister(run.req.TaskID)
	}

	result, err := ag.ThinkAct(ctx)
	if err != nil {
		if result != nil {
			return result, err
		}
		return nil, err
	}

	return result, nil
}

func formatUsageActivity(inputTokens, outputTokens int) string {
	return fmt.Sprintf("Usage: input=%d output=%d", inputTokens, outputTokens)
}

func (e *Executor) logRunCompletion(run *preparedRun, result *core.Result, success bool) {
	logFields := []zap.Field{
		zap.String("agent", run.cfg.displayName),
		zap.String("stopReason", string(result.StopReason)),
		zap.Int("steps", result.Steps),
		zap.Int("inputTokens", result.InputTokens),
		zap.Int("outputTokens", result.OutputTokens),
	}
	if success {
		log.Logger().Info("Agent completed", logFields...)
		return
	}
	log.Logger().Warn("Agent completed", logFields...)
}

func (e *Executor) buildAgentResult(run *preparedRun, result *core.Result) *AgentResult {
	success, errMsg := interpretStopReason(result, run.cfg.maxSteps)
	e.logRunCompletion(run, result, success)
	run.settleWorkspace()

	agentSessionID, agentTranscriptPath := e.persistSubagentSession(
		run.cfg.displayName,
		run.cfg.modelID,
		run.req.Description,
		result.Messages,
	)
	e.fireSubagentStop(run.req, run.hookID, agentTranscriptPath, result.Content)

	content := result.Content
	if run.keptWorktreePath != "" {
		content = appendWorktreeNote(content, run.keptWorktreePath)
	}

	return &AgentResult{
		AgentID:        agentSessionID,
		AgentName:      run.cfg.displayName,
		TranscriptPath: agentTranscriptPath,
		Model:          run.cfg.modelID,
		Success:        success,
		Content:        content,
		Messages:       result.Messages,
		StepCount:      result.Steps,
		ToolUses:       result.ToolUses,
		TokenUsage:     llm.Usage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens},
		Duration:       time.Since(run.startedAt),
		Activity:       append([]string(nil), run.trace...),
		WorktreePath:   run.keptWorktreePath,
		Error:          errMsg,
	}
}

// buildCancelledAgentResult preserves a cancelled run's partial work: the
// conversation is persisted (so the run is resumable), the stop hook fires,
// and the partial content plus any preserved worktree travel back to the
// caller alongside the error.
func (e *Executor) buildCancelledAgentResult(run *preparedRun, result *core.Result) *AgentResult {
	if result == nil || result.StopReason != core.StopCancelled {
		return nil
	}
	run.settleWorkspace()

	agentSessionID, agentTranscriptPath := e.persistSubagentSession(
		run.cfg.displayName,
		run.cfg.modelID,
		run.req.Description,
		result.Messages,
	)
	e.fireSubagentStop(run.req, run.hookID, agentTranscriptPath, result.Content)

	content := result.Content
	if run.keptWorktreePath != "" {
		content = appendWorktreeNote(content, run.keptWorktreePath)
	}

	return &AgentResult{
		AgentID:        agentSessionID,
		AgentName:      run.cfg.displayName,
		TranscriptPath: agentTranscriptPath,
		Model:          run.cfg.modelID,
		Success:        false,
		Content:        content,
		Messages:       result.Messages,
		StepCount:      result.Steps,
		ToolUses:       result.ToolUses,
		TokenUsage:     llm.Usage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens},
		Duration:       time.Since(run.startedAt),
		Activity:       append([]string(nil), run.trace...),
		WorktreePath:   run.keptWorktreePath,
		Error:          "agent cancelled",
	}
}

func appendWorktreeNote(content, worktreePath string) string {
	note := worktreePreservedNote(worktreePath)
	if content == "" {
		return note
	}
	return content + "\n\n" + note
}
