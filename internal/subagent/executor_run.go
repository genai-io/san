package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"
)

type preparedRun struct {
	originalPrompt string
	toolStarted    bool
	priorFailure   string
	native         *session.Session
	history        []core.Message
	checkpoint     *continuationState
	req            tool.AgentExecRequest
	cfg            *runConfig
	cwdMu          sync.RWMutex // Tool adapters read cwd from parallel goroutines.
	cwd            string
	system         core.System
	sideEffects    sync.Map // Each run owns handoffs, even when model call IDs repeat.
	// Batch handoffs are owned by the turn goroutine. All sibling tools use
	// the pre-batch cwd; the last cwd-changing call in request order wins,
	// regardless of completion order.
	batchTools   []string
	pendingTools int
	batchCwd     string
	batchCwdIdx  int
	startedAt    time.Time
	hookID       string
	// activity is the inspection trail, carried as display details by Agent.
	// Model, mode, usage, and spinner status go only to the live display.
	activity     []string
	inputTokens  int
	outputTokens int
}

// recordActivity keeps an inspection line and streams it to the worker's UI.
func (r *preparedRun) recordActivity(msg string) {
	r.activity = append(r.activity, msg)
	r.streamActivity(msg)
}

// streamActivity streams a line to the UI's OnActivity callback only — model,
// mode, and usage status that would be noise in the parent's result.
func (r *preparedRun) streamActivity(msg string) {
	if r.req.OnActivity != nil {
		r.req.OnActivity(msg)
	}
}

func (r *preparedRun) recordUsage(resp *ai.Response) {
	if r.req.OnActivity == nil || resp == nil {
		return
	}
	r.inputTokens += resp.Usage.Input
	r.outputTokens += resp.Usage.Output
	if r.inputTokens > 0 || r.outputTokens > 0 {
		r.streamActivity(formatUsageActivity(r.inputTokens, r.outputTokens))
	}
}

func (r *preparedRun) currentCwd() string {
	r.cwdMu.RLock()
	defer r.cwdMu.RUnlock()
	return r.cwd
}

func (r *preparedRun) storeSideEffect(id string, effect any) {
	r.sideEffects.Store(id, effect)
}

func (r *preparedRun) finishCwdHandoff(id string, canceled bool) {
	effect, _ := r.sideEffects.LoadAndDelete(id)
	handoff, _ := effect.(map[string]any)
	cwd, _ := handoff["cwd"].(string)
	index := slices.Index(r.batchTools, id)
	if !canceled && filepath.IsAbs(cwd) && index >= 0 && index >= r.batchCwdIdx {
		r.batchCwd, r.batchCwdIdx = cwd, index
	}
	if r.pendingTools > 0 {
		r.pendingTools--
	}
	if r.pendingTools != 0 || canceled || r.batchCwd == "" {
		return
	}
	r.cwdMu.Lock()
	r.cwd = r.batchCwd
	r.cwdMu.Unlock()
	if r.system != nil {
		r.system.Use(system.EnvironmentSection(system.Environment{Cwd: r.batchCwd, Shell: proc.DefaultShellName()}), "subagent:cwd")
	}
	r.batchCwd = ""
}

func (r *preparedRun) forwardEvent(ctx context.Context, ev core.Event) {
	// Legacy tools can turn cancellation into an ordinary exit error. Check the
	// worker context too, without hiding tool-local timeouts or run deadlines.
	canceled := ctx.Err() == context.Canceled
	switch event := ev.(type) {
	case sdkagent.InferenceUpdate:
		if r.req.OnTextDelta != nil {
			if fragment := event.Text(); fragment != "" {
				r.req.OnTextDelta(fragment)
			}
		}
	case sdkagent.InferenceEnd:
		if !canceled && event.Err != nil && !errors.Is(event.Err, context.Canceled) {
			r.recordActivity(fmt.Sprintf("inference attempt %d failed: %v", event.Attempt, event.Err))
		}
		if r.req.OnTextEnd != nil {
			r.req.OnTextEnd(event.Err)
		}
		r.recordUsage(event.Response)
	case sdkagent.ToolStart:
		r.toolStarted = true
		if r.pendingTools == 0 {
			r.batchTools = nil
			r.batchCwd, r.batchCwdIdx = "", -1
		}
		r.batchTools = append(r.batchTools, event.ID)
		r.pendingTools++
		if r.req.OnToolStart != nil {
			r.req.OnToolStart(event.ID, r.toolCallLabel(event.Name, event.Args))
		}
	case sdkagent.ToolEnd:
		// Every worker owns its adapter handoff, even without UI callbacks or
		// when cancellation suppresses error notices. Never leave it for main.
		r.finishCwdHandoff(event.ID, canceled)
		report := !canceled && event.Err != nil && !errors.Is(event.Err, context.Canceled)
		if report || r.req.OnToolResult != nil {
			label := r.toolCallLabel(event.Name, event.Args)
			if report {
				// Keep recoverable failures with the worker. Structured views
				// already finish their tool row through OnToolResult below.
				line := fmt.Sprintf("%s failed: %v · %s", event.Name, event.Err, label)
				r.activity = append(r.activity, line)
				if r.req.OnToolResult == nil {
					r.streamActivity(line)
				}
			}
			if r.req.OnToolResult != nil {
				r.req.OnToolResult(event.ID, label, sdkagent.ResultText(event.Result, event.Err), event.Err)
			}
		}
	}
}

func (r *preparedRun) toolCallLabel(name, rawArgs string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil || args == nil {
		return name
	}
	return formatToolActivityLimit(name, args, r.req.ActivityMaxChars)
}

func (e *Executor) prepareRun(ctx context.Context, req tool.AgentExecRequest) (*preparedRun, error) {
	if err := e.validateRequest(req); err != nil {
		return nil, err
	}

	cfg, err := e.prepareRunConfig(ctx, req)
	if err != nil {
		return nil, err
	}

	return &preparedRun{
		originalPrompt: e.taskPrompt(cfg, req),
		req:            req,
		cfg:            cfg,
		cwd:            e.cwd,
		startedAt:      time.Now(),
		hookID:         "a" + generateShortID(),
		activity:       make([]string, 0, 16),
	}, nil
}

func (e *Executor) logRunStart(run *preparedRun) {
	log.Logger().Info("Starting agent execution",
		zap.String("agent", run.cfg.displayName),
		zap.String("description", run.req.Description),
		zap.Int("maxSteps", run.cfg.maxSteps),
	)
}

func (e *Executor) executePreparedRun(ctx context.Context, run *preparedRun) (out *core.Result, runErr error) {
	if run.native == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := e.openNative(run); err != nil {
			return nil, err
		}
	}
	if run.native != nil {
		defer func() {
			if run.checkpoint != nil {
				run.checkpoint.cwd = run.currentCwd()
			}
			persistErr := errors.Join(run.native.Err(), run.native.Close())
			if run.checkpoint != nil {
				run.checkpoint.persistenceErr = persistErr
			}
			runErr = errors.Join(runErr, persistErr)
		}()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var onToolExec func(string, map[string]any)
	if run.req.OnActivity != nil {
		run.streamActivity(fmt.Sprintf("Model: %s", run.cfg.modelID))
		run.streamActivity(fmt.Sprintf("Mode: %s · max %d steps", displayPermissionMode(run.cfg.permMode), run.cfg.maxSteps))
		onToolExec = func(name string, params map[string]any) {
			msg := formatToolActivityLimit(name, params, run.req.ActivityMaxChars)
			if run.req.OnToolStart != nil {
				// The structured ToolStart row is already live. Keep the
				// inspection trail without streaming a second call row.
				run.activity = append(run.activity, msg)
			} else {
				run.recordActivity(msg)
			}
		}
	}
	onEvent := func(ev core.Event) {
		if run.native != nil {
			if _, stopped := ev.(core.AgentStopped); !stopped {
				run.native.RecordEvent(ev)
			}
		}
		run.forwardEvent(ctx, ev)
	}
	ag, cleanupAgent, err := e.buildAgent(ctx, run, onToolExec, onEvent)
	if err != nil {
		return nil, err
	}
	defer cleanupAgent()

	if run.req.ResumeTaskID != "" {
		ag.SetMessages(run.history)
		ag.Append(core.UserMessage(fmt.Sprintf("Continuation (previous execution: %s; current cwd: %s). Verify current filesystem/external state before retrying any interrupted tool with unknown outcome.\n", run.priorFailure, run.currentCwd())+run.req.Prompt, nil))
	} else if err := e.loadConversation(ag, ctx, run.cfg, run.req); err != nil {
		return nil, err
	}
	if run.req.OnActivity != nil {
		run.streamActivity("Thinking...")
	}

	// Stamp the running worker's own broker address onto the context so its
	// SendMessage calls are attributed to it, not defaulted to "main". A
	// background run uses its task id (and registers it below so main can
	// message it mid-flight); a foreground run has no task id, so it borrows
	// its hook id purely as a sender identity — nothing routes back to a
	// foreground worker, which blocks its spawning turn.
	agentAddr := run.req.TaskID
	if agentAddr == "" {
		agentAddr = run.hookID
	}
	ctx = tool.WithAgentID(ctx, agentAddr)
	ctx = task.WithSessionID(ctx, e.parentSessionID)

	// A background subagent registers its task id with the broker for the
	// length of the run. A routed message enters its conversation at the next
	// step boundary, and its run does not end with one unread.
	if run.req.TaskID != "" {
		broker.Register(run.req.TaskID, func(m broker.Message) bool {
			ag.Append(core.UserMessage(m.Content, nil))
			return true
		})
		defer broker.Unregister(run.req.TaskID)
	}

	result, err := ag.ThinkAct(ctx)
	// One safe fallback, never an outer replay of a tool-bearing execution.
	// Reuse the exact conversation and native writer; do not append the task
	// or continuation instruction again.
	if ctx.Err() == nil && !run.toolStarted && shouldRetryWithParentModel(err, run.cfg.modelID, e.parentModelID) {
		messages := ag.Messages()
		run.cfg.provider, run.cfg.modelID = e.provider, e.parentModelID
		fallback, cleanupFallback, buildErr := e.buildAgent(ctx, run, onToolExec, onEvent)
		if buildErr != nil {
			return result, buildErr
		}
		defer cleanupFallback()
		ag = fallback
		ag.SetMessages(messages)
		result, err = ag.ThinkAct(ctx)
	}
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
		zap.Int("inputTokens", result.Usage.Input),
		zap.Int("outputTokens", result.Usage.Output),
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
	return e.finalizeResult(run, result, success, errMsg)
}

// buildUnfinishedAgentResult preserves the partial work of a run that ended
// without completing: the conversation is persisted, the stop hook fires, and
// the partial content travels back to the caller alongside the error. Covers
// both ways a turn can end early — the user cancelled it, or inference failed
// past the retry budget — since either way the steps already taken are worth
// keeping. A nil Result means there is nothing to preserve.
func (e *Executor) buildUnfinishedAgentResult(run *preparedRun, result *core.Result) *AgentResult {
	if result == nil {
		return nil
	}
	if result.StopReason != core.StopCanceled && result.StopReason != core.StopError {
		return nil
	}
	_, errMsg := interpretStopReason(result, run.cfg.maxSteps)
	return e.finalizeResult(run, result, false, errMsg)
}

// finalizeResult persists the session, fires the stop hook, and projects the
// run into an AgentResult. Both the success and cancelled paths share it —
// they differ only in Success/Error.
func (e *Executor) finalizeResult(run *preparedRun, result *core.Result, success bool, errMsg string) *AgentResult {
	var agentSessionID, agentTranscriptPath string
	if run.native != nil {
		agentSessionID, agentTranscriptPath = run.native.ID(), run.native.TranscriptPath()
	} else {
		agentSessionID, agentTranscriptPath = e.persistSubagentSession(
			run.cfg.displayName,
			run.cfg.modelID,
			run.req.Description,
			result.Messages,
		)
	}
	e.fireSubagentStop(run.req, run.hookID, agentTranscriptPath, result.Content)

	return &AgentResult{
		AgentID:        agentSessionID,
		AgentName:      run.cfg.config.Name,
		TranscriptPath: agentTranscriptPath,
		Model:          run.cfg.modelID,
		Success:        success,
		Content:        result.Content,
		Messages:       result.Messages,
		StepCount:      result.Steps,
		ToolUses:       result.ToolUses,
		TokenUsage:     result.Usage,
		Duration:       time.Since(run.startedAt),
		Activity:       append([]string(nil), run.activity...),
		Error:          errMsg,
	}
}
