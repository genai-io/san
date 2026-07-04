package subagent

import (
	"context"
	"fmt"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool"
	"go.uber.org/zap"
)

type preparedRun struct {
	req              tool.AgentExecRequest
	cfg              *runConfig
	cwd              string
	startedAt        time.Time
	hookID           string
	activity         []string
	inputTokens      int
	outputTokens     int
	cleanupWorkspace func()
}

func (r *preparedRun) close() {
	if r != nil && r.cleanupWorkspace != nil {
		r.cleanupWorkspace()
	}
}

func (r *preparedRun) sendActivity(msg string) {
	r.activity = append(r.activity, msg)
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
		r.sendActivity(formatUsageActivity(r.inputTokens, r.outputTokens))
	}
}

func (e *Executor) prepareRun(ctx context.Context, req tool.AgentExecRequest) (*preparedRun, error) {
	if err := e.validateRequest(req); err != nil {
		return nil, err
	}

	agentCwd, cleanupWorkspace, err := e.prepareWorkspace(req)
	if err != nil {
		return nil, err
	}

	cfg, err := e.prepareRunConfig(ctx, req)
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}

	return &preparedRun{
		req:              req,
		cfg:              cfg,
		cwd:              agentCwd,
		startedAt:        time.Now(),
		hookID:           "a" + generateShortID(),
		activity:         make([]string, 0, 16),
		cleanupWorkspace: cleanupWorkspace,
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
		modelMsg := fmt.Sprintf("Model: %s", run.cfg.modelID)
		run.sendActivity(modelMsg)
		startMsg := fmt.Sprintf("Mode: %s · max %d steps", displayPermissionMode(run.cfg.permMode), run.cfg.maxSteps)
		run.sendActivity(startMsg)
		onToolExec = func(name string, params map[string]any) {
			msg := formatToolActivity(name, params)
			run.sendActivity(msg)
		}
	}
	ag, cleanupAgent, err := e.buildAgent(ctx, run.cfg, run.cwd, onToolExec, func(ev core.Event) {
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
		run.sendActivity("Thinking...")
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

	agentSessionID, agentTranscriptPath := e.persistSubagentSession(
		run.cfg.displayName,
		run.cfg.modelID,
		run.req.Description,
		result.Messages,
	)
	e.fireSubagentStop(run.req, run.hookID, agentSessionID, agentTranscriptPath, result.Content)

	return &AgentResult{
		AgentID:        agentSessionID,
		AgentName:      run.cfg.displayName,
		TranscriptPath: agentTranscriptPath,
		Model:          run.cfg.modelID,
		Success:        success,
		Content:        result.Content,
		Messages:       result.Messages,
		StepCount:      result.Steps,
		ToolUses:       result.ToolUses,
		TokenUsage:     llm.Usage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens},
		Duration:       time.Since(run.startedAt),
		Activity:       append([]string(nil), run.activity...),
		Error:          errMsg,
	}
}

func (e *Executor) buildCancelledAgentResult(run *preparedRun, result *core.Result) *AgentResult {
	if result == nil || result.StopReason != core.StopCancelled {
		return nil
	}

	return &AgentResult{
		AgentName:  run.cfg.displayName,
		Model:      run.cfg.modelID,
		Success:    false,
		Content:    result.Content,
		Messages:   result.Messages,
		StepCount:  result.Steps,
		ToolUses:   result.ToolUses,
		TokenUsage: llm.Usage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens},
		Duration:   time.Since(run.startedAt),
		Activity:   append([]string(nil), run.activity...),
		Error:      "agent cancelled",
	}
}
