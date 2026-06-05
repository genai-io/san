package subagent

import (
	"context"
	"fmt"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"go.uber.org/zap"
)

type preparedRun struct {
	req              AgentRequest
	cfg              *runConfig
	cwd              string
	startedAt        time.Time
	hookID           string
	progress         []string
	inputTokens      int
	outputTokens     int
	cleanupWorkspace func()
}

func (r *preparedRun) close() {
	if r != nil && r.cleanupWorkspace != nil {
		r.cleanupWorkspace()
	}
}

func (r *preparedRun) sendProgress(msg string) {
	r.progress = append(r.progress, msg)
	if r.req.OnProgress != nil {
		r.req.OnProgress(msg)
	}
}

func (r *preparedRun) recordUsage(resp *core.InferResponse) {
	if r.req.OnProgress == nil || resp == nil {
		return
	}
	r.inputTokens += resp.TokensIn
	r.outputTokens += resp.TokensOut
	if r.inputTokens > 0 || r.outputTokens > 0 {
		r.sendProgress(formatUsageProgress(r.inputTokens, r.outputTokens))
	}
}

func (e *Executor) prepareRun(req AgentRequest) (*preparedRun, error) {
	if err := e.validateRequest(req); err != nil {
		return nil, err
	}

	agentCwd, cleanupWorkspace, err := e.prepareWorkspace(req)
	if err != nil {
		return nil, err
	}

	cfg, err := e.prepareRunConfig(req)
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
		progress:         make([]string, 0, 16),
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
	if run.req.OnProgress != nil {
		modelMsg := fmt.Sprintf("Model: %s", run.cfg.modelID)
		run.sendProgress(modelMsg)
		startMsg := fmt.Sprintf("Mode: %s · max %d steps", displayPermissionMode(run.cfg.permMode), run.cfg.maxSteps)
		run.sendProgress(startMsg)
		onToolExec = func(name string, params map[string]any) {
			msg := formatToolProgress(name, params)
			run.sendProgress(msg)
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
	if run.req.OnProgress != nil {
		run.sendProgress("Thinking...")
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

func formatUsageProgress(inputTokens, outputTokens int) string {
	return fmt.Sprintf("Usage: input=%d output=%d", inputTokens, outputTokens)
}

func (e *Executor) logRunCompletion(run *preparedRun, result *core.Result, success bool) {
	logFields := []zap.Field{
		zap.String("agent", run.cfg.displayName),
		zap.String("stopReason", string(result.StopReason)),
		zap.Int("steps", result.Steps),
		zap.Int("inputTokens", result.TokensIn),
		zap.Int("outputTokens", result.TokensOut),
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
		TokenUsage:     llm.TokenUsage{InputTokens: result.TokensIn, OutputTokens: result.TokensOut, TotalTokens: result.TokensIn + result.TokensOut},
		Duration:       time.Since(run.startedAt),
		Progress:       append([]string(nil), run.progress...),
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
		TokenUsage: llm.TokenUsage{InputTokens: result.TokensIn, OutputTokens: result.TokensOut, TotalTokens: result.TokensIn + result.TokensOut},
		Duration:   time.Since(run.startedAt),
		Progress:   append([]string(nil), run.progress...),
		Error:      "agent cancelled",
	}
}
