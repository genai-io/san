package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	glog "github.com/genai-io/gen-code/internal/log"
)

// agent is the default Agent implementation.
type agent struct {
	id                string
	agentType         string
	color             string
	system            System
	tools             Tools
	compactFunc       func(ctx context.Context, msgs []Message) (string, error)
	llm               LLM
	cwd               string
	maxTurns          int
	maxOutputRecovery int
	inbox             chan Message
	outbox            chan Event
	onEvent           func(Event)

	mu       sync.RWMutex
	messages []Message // conversation history

	closed atomic.Bool // guards outbox writes after close
}

type agentToolTask struct {
	call ToolCall
	tool Tool
}

type toolTaskOutput struct {
	content string
	err     error
}

func (a *agent) ID() string            { return a.id }
func (a *agent) System() System        { return a.system }
func (a *agent) Tools() Tools          { return a.tools }
func (a *agent) Inbox() chan<- Message { return a.inbox }
func (a *agent) Outbox() <-chan Event  { return a.outbox }
func (a *agent) Messages() []Message   { return a.snapshot() }

func (a *agent) SetMessages(msgs []Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = make([]Message, len(msgs))
	copy(a.messages, msgs)
}

// Append adds a message to the conversation and fires the OnMessage hook.
func (a *agent) Append(ctx context.Context, msg Message) {
	a.ingest(ctx, msg)
}

// Run is the agent's main loop: wait for input → think+act → repeat.
// Outbox is closed when Run returns. Inbox is NOT closed (caller owns it).
func (a *agent) Run(ctx context.Context) error {
	a.emit(ctx, StartEvent(a.id))

	var runErr error
	defer func() {
		// StopEvent must be delivered even on context cancellation,
		// so use emitFinal which bypasses ctx.Done().
		a.emitFinal(StopEvent(a.id, runErr))
		a.closed.Store(true)

		if a.outbox != nil {
			close(a.outbox)
		}
	}()

	for {
		glog.QueueLog("agent.Run: waitForInput blocking...")
		if err := a.waitForInput(ctx); err != nil {
			if err == errStopped {
				return nil
			}
			runErr = err
			return err
		}
		glog.QueueLog("agent.Run: waitForInput received message")

		for {
			glog.QueueLog("agent.Run: starting ThinkAct")
			result, err := a.ThinkAct(ctx)
			if result != nil {
				glog.QueueLog("agent.Run: ThinkAct done, emitting TurnEvent")
				a.emit(ctx, TurnEvent(a.id, *result))
			}
			if err != nil {
				glog.QueueLog("agent.Run: ThinkAct error: %v", err)
				if err == errStopped {
					return nil
				}
				runErr = err
				return err
			}

			n, drainErr := a.drainInbox(ctx)
			if drainErr != nil {
				if drainErr == errStopped {
					return nil
				}
				runErr = drainErr
				return drainErr
			}
			glog.QueueLog("agent.Run: post-ThinkAct drain n=%d", n)
			if n == 0 {
				break
			}
		}
	}
}

var errStopped = errors.New("stopped")

// TruncatedResumePrompt is injected when generation stops at the output limit
// and the caller wants the model to continue in the next turn.
const TruncatedResumePrompt = "Your response was truncated due to output token limits. Resume directly from where you left off. Do not repeat any content."

// waitForInput blocks until at least one message arrives, then drains remaining.
func (a *agent) waitForInput(ctx context.Context) error {
	// Block until first message
	select {
	case msg, ok := <-a.inbox:
		if !ok || msg.Signal == SigStop {
			return errStopped
		}
		a.ingest(ctx, msg)
	case <-ctx.Done():
		return ctx.Err()
	}

	// Drain remaining (non-blocking)
	for {
		select {
		case msg, ok := <-a.inbox:
			if !ok || msg.Signal == SigStop {
				return errStopped
			}
			a.ingest(ctx, msg)
		default:
			return nil
		}
	}
}

// ingest notifies hooks and appends a message (with text + images) to conversation.
func (a *agent) ingest(ctx context.Context, msg Message) {
	a.emit(ctx, MessageEvent(msg))
	if msg.Signal == "" {
		a.append(msg)
	}
}

// ThinkAct runs one full inference-action cycle until end_turn.
// Returns the result directly — the caller decides whether to emit TurnEvent.
func (a *agent) ThinkAct(ctx context.Context) (*Result, error) {
	var turns, toolUses, tokensIn, tokensOut, lastInputTokens, lastPromptTextLen int
	var maxOutputRecoveryCount int

	makeResult := func(content string, stop StopReason, detail string) *Result {
		return &Result{
			Content: content, Messages: a.snapshot(),
			Turns: turns, ToolUses: toolUses, TokensIn: tokensIn, TokensOut: tokensOut,
			StopReason: stop, StopDetail: detail,
		}
	}

	for {
		if ctx.Err() != nil {
			return makeResult("", StopCancelled, ""), ctx.Err()
		}

		// Max turns guard
		if a.maxTurns > 0 && turns >= a.maxTurns {
			return makeResult("max turns reached", StopMaxTurns, ""), nil
		}

		// Between turns: drain any new inbox messages (non-blocking)
		if turns > 0 {
			if _, err := a.drainInbox(ctx); err != nil {
				return nil, err
			}
		}

		currentPromptTextLen := len(BuildConversationText(a.snapshot()))

		// Pre-infer compaction: estimate the next prompt size from the latest
		// known prompt-token count and current conversation growth.
		if a.compactFunc != nil && lastInputTokens > 0 {
			estimatedInputTokens := estimatePromptTokens(lastInputTokens, lastPromptTextLen, currentPromptTextLen)
			if limit := a.llm.InputLimit(); limit > 0 && NeedsCompaction(estimatedInputTokens, limit) {
				if a.compact(ctx) {
					continue
				}
			}
		}

		resp, err := a.streamInfer(ctx)
		if err != nil {
			// Reactive compaction: if prompt too long, compact and retry
			if a.compactFunc != nil && isPromptTooLong(err) && a.compact(ctx) {
				continue
			}
			return nil, err
		}

		turns++
		lastInputTokens = resp.TokensIn
		lastPromptTextLen = currentPromptTextLen
		tokensIn += resp.TokensIn
		tokensOut += resp.TokensOut

		a.emit(ctx, PostInferEvent(a.id, resp))
		a.append(Message{
			Role: RoleAssistant, From: a.id,
			Content: resp.Content, Thinking: resp.Thinking,
			ThinkingSignature: resp.ThinkingSignature,
			ToolCalls:         resp.ToolCalls,
		})

		// Max tokens recovery — output truncated, ask LLM to continue
		if resp.StopReason == StopMaxTokens && len(resp.ToolCalls) == 0 {
			maxRecovery := a.maxOutputRecovery
			if maxRecovery <= 0 {
				maxRecovery = 3
			}
			if maxOutputRecoveryCount >= maxRecovery {
				return makeResult(resp.Content, StopMaxOutputRecoveryExhausted, ""), nil
			}
			maxOutputRecoveryCount++
			a.append(Message{Role: RoleUser, From: "system", Content: TruncatedResumePrompt})
			continue
		}

		// No tool calls → end turn
		if len(resp.ToolCalls) == 0 {
			return makeResult(resp.Content, StopEndTurn, ""), nil
		}

		// Execute tool calls
		toolUses += a.execTools(ctx, resp.ToolCalls)
	}
}

func estimatePromptTokens(lastInputTokens, lastPromptTextLen, currentPromptTextLen int) int {
	if lastInputTokens <= 0 {
		return 0
	}
	if lastPromptTextLen <= 0 || currentPromptTextLen <= 0 {
		return lastInputTokens
	}
	estimated := (lastInputTokens * currentPromptTextLen) / lastPromptTextLen
	if estimated < lastInputTokens {
		return lastInputTokens
	}
	return estimated
}

// execTools runs tool calls in three phases:
//  1. Resolve — emit PreTool event, look up tool
//  2. Execute — parallel for read-only batches, sequential when side effects are possible
//  3. Record results — sequential, in original call order
//
// Permission checking is handled by the tool decorator (tool.WithPermission),
// not by the agent. See docs/gen-permission.md.
func (a *agent) execTools(ctx context.Context, calls []ToolCall) int {
	var tasks []agentToolTask
	for _, tc := range calls {
		if ctx.Err() != nil {
			break
		}
		a.emit(ctx, PreToolEvent(tc))
		t := a.tools.Get(tc.Name)
		if t == nil {
			a.appendResult(tc, fmt.Sprintf("unknown tool: %s", tc.Name), true)
			continue
		}
		tasks = append(tasks, agentToolTask{tc, t})
	}
	if len(tasks) == 0 {
		return 0
	}

	// Phase 2: Execute (parallel only for read-only batches)
	results := make([]toolTaskOutput, len(tasks))
	if len(tasks) == 1 || !canExecuteToolBatchInParallel(tasks) {
		for i, t := range tasks {
			results[i] = executeToolTask(ctx, t)
		}
	} else {
		var wg sync.WaitGroup
		for i, t := range tasks {
			wg.Add(1)
			go func(i int, t agentToolTask) {
				defer wg.Done()
				results[i] = executeToolTask(ctx, t)
			}(i, t)
		}
		wg.Wait()
	}

	// Phase 3: Record results in order + PostTool hooks
	var toolUses int
	for i, t := range tasks {
		r := results[i]
		if r.err != nil {
			a.appendResult(t.call, r.err.Error(), true)
			a.emit(ctx, PostToolEvent(ToolResult{
				ToolCallID: t.call.ID, ToolName: t.call.Name, Content: r.err.Error(), IsError: true,
			}))
			continue
		}
		toolUses++
		a.appendResult(t.call, r.content, false)
		a.emit(ctx, PostToolEvent(ToolResult{
			ToolCallID: t.call.ID, ToolName: t.call.Name, Content: r.content,
		}))
	}
	return toolUses
}

func executeToolTask(ctx context.Context, t agentToolTask) (result toolTaskOutput) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("core/agent: tool %s panicked: %v\n%s", t.call.Name, r, debug.Stack())
			result = toolTaskOutput{"", fmt.Errorf("tool %s panicked: %v", t.call.Name, r)}
		}
	}()
	params, _ := ParseToolInput(t.call.Input)
	execCtx := WithToolCallID(ctx, t.call.ID)
	content, err := t.tool.Execute(execCtx, params)
	return toolTaskOutput{content, err}
}

func canExecuteToolBatchInParallel(tasks []agentToolTask) bool {
	for _, t := range tasks {
		if !isReadOnlyToolCall(t.call.Name) {
			return false
		}
	}
	return true
}

func isReadOnlyToolCall(name string) bool {
	switch name {
	case "Read", "Glob", "Grep", "WebFetch", "WebSearch", "LSP", "TaskOutput", "AgentOutput":
		return true
	default:
		return false
	}
}

// CompactMaxTokens is the max output tokens for compaction LLM calls.
const CompactMaxTokens = 4096

// FormatCompactSummary formats a compaction summary for injection as a user message.
func FormatCompactSummary(summary string) string {
	return "Previous context:\n" + summary
}

// compact calls CompactFunc and replaces messages with the summary.
// Returns true if compaction succeeded.
func (a *agent) compact(ctx context.Context) bool {
	msgs := a.snapshot()
	if len(msgs) < 3 {
		return false
	}
	summary, err := a.compactFunc(ctx, msgs)
	if err != nil || summary == "" {
		return false
	}
	originalCount := len(msgs)
	a.SetMessages([]Message{UserMessage(FormatCompactSummary(summary), nil)})
	a.emit(ctx, CompactEvent(a.id, CompactInfo{Summary: summary, OriginalCount: originalCount}))
	return true
}

// isPromptTooLong checks if an error indicates the prompt exceeds the model's limit.
func isPromptTooLong(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "prompt_too_long")
}

// --- context keys ---

type contextKey string

const toolCallIDKey contextKey = "tool_call_id"

// WithToolCallID returns a context carrying the given tool call ID.
func WithToolCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolCallIDKey, id)
}

// ToolCallIDFromContext extracts the tool call ID from the context.
func ToolCallIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(toolCallIDKey).(string); ok {
		return id
	}
	return ""
}

// --- internals ---

// streamInfer calls the LLM, streams chunks to outbox, returns the final response.
//
// Emits PreInferEvent (with input digests) before the LLM call so observers
// can record exactly what was sent without copying the bytes on every turn.
func (a *agent) streamInfer(ctx context.Context) (*InferResponse, error) {
	sys := a.system.Prompt()
	msgs := a.snapshot()
	tools := a.tools.Schemas()

	a.emit(ctx, PreInferEvent(a.id, InferenceContext{
		SystemDigest: sha256Hex([]byte(sys)),
		ToolsDigest:  toolsDigest(tools),
		MessageIDs:   messageIDs(msgs),
	}))

	chunks, err := a.llm.Infer(ctx, InferRequest{
		System:   sys,
		Messages: msgs,
		Tools:    tools,
	})
	if err != nil {
		return nil, fmt.Errorf("infer: %w", err)
	}

	var resp *InferResponse
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				if resp == nil {
					return nil, fmt.Errorf("infer: stream closed without response")
				}
				return resp, nil
			}
			if chunk.Err != nil {
				return nil, fmt.Errorf("infer: %w", chunk.Err)
			}
			if chunk.Text != "" || chunk.Thinking != "" || chunk.Done {
				a.emit(ctx, ChunkEvent(a.id, chunk))
			}
			if chunk.Done {
				resp = chunk.Response
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// emit sends an event to the outbox for external observation.
// No-op when outbox is nil (subagent direct path).
// Blocks if outbox is full (backpressure). Skips if outbox is closed or ctx is cancelled.
func (a *agent) emit(ctx context.Context, event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
	if a.outbox == nil || a.closed.Load() {
		return
	}
	select {
	case a.outbox <- event:
	case <-ctx.Done():
	}
}

// emitTelemetry delivers a fire-and-forget event: synchronously to onEvent,
// non-blocking to the outbox (dropped if full). Used for events whose
// consumers tolerate misses (system changes, hot-path tracing) and which can
// fire from goroutines without a useful ctx (e.g. system observer callbacks).
func (a *agent) emitTelemetry(event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
	if a.outbox == nil || a.closed.Load() {
		return
	}
	select {
	case a.outbox <- event:
	default:
	}
}

// emitFinal sends a critical event that must be delivered even on ctx cancellation.
// Used for StopEvent — consumers rely on it for cleanup/session saving.
// No-op when outbox is nil. Blocks up to 5 seconds; logs a warning if delivery fails.
func (a *agent) emitFinal(event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
	if a.outbox == nil || a.closed.Load() {
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case a.outbox <- event:
	case <-timer.C:
		log.Printf("core/agent: failed to deliver %s event (outbox full for 5s)", event.Type)
	}
}

// drainInbox non-blocking reads ONE pending inbox message.
// Returns 1 if a message was consumed, 0 if none available.
// Each message gets its own ThinkAct cycle so the TUI can pair
// each user message with its response.
func (a *agent) drainInbox(ctx context.Context) (int, error) {
	select {
	case msg, ok := <-a.inbox:
		if !ok || msg.Signal == SigStop {
			return 0, errStopped
		}
		a.ingest(ctx, msg)
		return 1, nil
	default:
		return 0, nil
	}
}

func (a *agent) append(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
}

func (a *agent) snapshot() []Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cp := make([]Message, len(a.messages))
	copy(cp, a.messages)
	return cp
}

func (a *agent) appendResult(tc ToolCall, content string, isError bool) {
	a.append(Message{
		Role: RoleTool, From: tc.Name, Content: content,
		ToolResult: &ToolResult{ToolCallID: tc.ID, ToolName: tc.Name, Content: content, IsError: isError},
	})
}
