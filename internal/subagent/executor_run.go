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

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"
)

type preparedRun struct {
	hookFactory       func(*session.Session, llm.Provider, string) (hook.Handler, func(llm.Provider, string), func())
	rebindHooks       func(llm.Provider, string)
	stopHooks         func()
	hooks             hook.Handler
	hooksBound        bool
	sourceAfter       uint64 // Accepted prefix before this execution; never cite an older answer.
	originalPrompt    string
	toolStarted       bool
	continuationInput *core.Message // exact preaccepted prompt, retained across async launch
	priorFailure      string
	// Capture original ownership before scheduling child work.
	store       *session.Store
	parent      *session.Session
	recorder    *session.Recorder
	recording   *session.Session
	history     []core.Message
	checkpoint  *continuationState
	req         tool.AgentExecRequest
	cfg         *runConfig
	cwdMu       sync.RWMutex // Tool adapters read cwd from parallel goroutines.
	cwd         string
	system      core.System
	sideEffects sync.Map // Each run owns handoffs, even when model call IDs repeat.
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

func (r *preparedRun) parentID() string {
	if r.parent != nil {
		return r.parent.ID()
	}
	return ""
}

func (r *preparedRun) identity() toolresult.AgentIdentity {
	id := r.req.TaskID
	if id == "" {
		id = r.hookID
	}
	callID := r.req.ParentToolCallID
	if r.checkpoint != nil {
		callID = r.checkpoint.req.ParentToolCallID
	}
	identity := toolresult.AgentIdentity{ID: id, Name: r.cfg.config.Name, CallID: callID}
	if r.recording != nil {
		identity.SessionID = r.recording.ID()
	} else if r.checkpoint != nil {
		identity.SessionID = r.checkpoint.sessionID
	}
	return identity
}

func (r *preparedRun) reportIdentity() {
	if report := r.req.OnIdentity; report != nil {
		r.req.OnIdentity = nil
		report(r.identity())
	}
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

func (r *preparedRun) finishCwdHandoff(ctx context.Context, id string, canceled bool) {
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
	if r.recorder != nil {
		if err := r.recorder.RecordState(ctx, session.SessionUpdatePayload{State: &session.StatePatch{ExecutionCWD: session.SetField(r.batchCwd)}}); err != nil {
			r.batchCwd = ""
			return
		}
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
		r.finishCwdHandoff(ctx, event.ID, canceled)
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
		hookFactory:    e.childHooks,
		hooks:          e.hooks,
		store:          e.store,
		parent:         e.parent,
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
	if run.recording == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := e.openNative(ctx, run); err != nil {
			return nil, err
		}
	}
	if run.recording != nil {
		snapshot := run.recording.Snapshot()
		events := snapshot.Events()
		if len(events) > 0 {
			run.sourceAfter = events[len(events)-1].Seq
		}
	}
	run.reportIdentity()
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
		if event, ok := ev.(sdkagent.ToolEnd); ok && e.postTool != nil {
			effect, _ := run.sideEffects.Load(event.ID)
			if err := e.postTool(run.hooks, core.ToolResult{ToolCallID: event.ID, ToolName: event.Name, Content: event.Result.Content, IsError: event.Err != nil}, effect); err != nil {
				_ = run.recorder.Fail(err) // Returned at the next admission or cleanup.
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
		ag.Append(*run.continuationInput)
	} else if err := e.loadConversation(ag, ctx, run.cfg, run.req); err != nil {
		return nil, err
	}
	if run.req.OnActivity != nil {
		run.streamActivity("Thinking...")
	}

	// Stamp the running worker's own broker address onto the context so its
	// SendMessage calls are attributed to it, not defaulted to "main". A
	// run uses its task id for both foreground and background execution.
	agentAddr := run.req.TaskID
	if agentAddr == "" {
		agentAddr = run.hookID
	}
	ctx = tool.WithAgentID(ctx, agentAddr)
	ctx = task.WithSessionID(ctx, run.parentID())

	inboxID := run.req.TaskID
	if e.tasks != nil {
		// Session-owned hosts do not publish worker addresses in the TUI broker.
		inboxID = ""
	}
	result, err := runWithInbox(ctx, ag, inboxID, run.cfg.maxSteps, run.recording)
	// One safe fallback, never an outer replay of a tool-bearing execution.
	// Reuse the exact conversation and native writer; do not append the task
	// or continuation instruction again.
	if ctx.Err() == nil && run.recording.Err() == nil && (run.recorder == nil || run.recorder.Err() == nil) && !run.toolStarted && shouldRetryWithParentModel(err, run.cfg.modelID, e.parentModelID) {
		messages := ag.Messages()
		run.cfg.provider, run.cfg.modelID = e.provider, e.parentModelID
		if run.recording != nil {
			if updateErr := run.recording.Append(ctx, session.Event{Type: session.EventSessionUpdate, Payload: session.SessionUpdatePayload{Config: &session.ConfigPatch{Provider: session.SetField(e.provider.Name()), Model: session.SetField(e.parentModelID)}}}); updateErr != nil {
				return result, updateErr
			}
		}
		if run.rebindHooks != nil {
			run.rebindHooks(run.cfg.provider, run.cfg.modelID)
		}
		fallback, cleanupFallback, buildErr := e.buildAgent(ctx, run, onToolExec, onEvent)
		if buildErr != nil {
			return result, buildErr
		}
		defer cleanupFallback()
		fallback.SetMessages(messages)
		result, err = runWithInbox(ctx, fallback, inboxID, run.cfg.maxSteps, run.recording)
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
	if run.recording != nil {
		agentSessionID, agentTranscriptPath = run.recording.ID(), run.recording.Path()
	}
	e.fireSubagentStop(run.hooks, run.req, run.hookID, agentTranscriptPath, result.Content)

	identity := run.identity()
	identity.SessionID = agentSessionID
	var sourceSeq uint64
	if run.recording != nil && result.Content != "" {
		messageID := ""
		for i := len(result.Messages) - 1; i >= 0; i-- {
			if result.Messages[i].Role == ai.RoleAssistant && result.Messages[i].Text() == result.Content {
				messageID = result.Messages[i].ID
				break
			}
		}
		snapshot := run.recording.Snapshot()
		for _, event := range snapshot.Events() {
			if event.Seq <= run.sourceAfter {
				continue
			}
			if payload, ok := event.Payload.(*session.MessageAppendPayload); ok {
				for _, message := range payload.Messages {
					if messageID != "" && message.ID == messageID {
						sourceSeq = event.Seq
					}
				}
			}
		}
	}
	return &AgentResult{
		SourceMessageSeq: sourceSeq,
		Identity:         identity,
		AgentID:          agentSessionID,
		AgentName:        run.cfg.config.Name,
		TranscriptPath:   agentTranscriptPath,
		Model:            run.cfg.modelID,
		Success:          success,
		Content:          result.Content,
		Messages:         result.Messages,
		StepCount:        result.Steps,
		ToolUses:         result.ToolUses,
		TokenUsage:       result.Usage,
		Duration:         time.Since(run.startedAt),
		Activity:         append([]string(nil), run.activity...),
		Error:            errMsg,
	}
}
