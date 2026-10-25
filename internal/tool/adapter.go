package tool

import (
	"context"
	"fmt"
	"sync"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// sideEffects stores HookResponse values keyed by tool call ID.
// The TUI retrieves these when handling PostTool events to apply
// environment side effects (cwd changes, file cache, background tasks).
var sideEffects sync.Map

// TODO(sdk-go): write tools with agent.ToolFunc and report progress with agent.Report; this adapter and tool/params.go then go.
// PopSideEffect retrieves and removes the HookResponse for a tool call.
// Returns nil if no side effect was stored.
func PopSideEffect(toolCallID string) any {
	val, ok := sideEffects.LoadAndDelete(toolCallID)
	if !ok {
		return nil
	}
	return val
}

// AskUserFunc poses a structured question to the user and returns their answer.
// The AskUserQuestion tool calls it mid-execution; the TUI layer supplies the
// implementation via AgentToUI.Ask().
type AskUserFunc func(ctx context.Context, req *QuestionRequest) (*QuestionResponse, error)

// AdaptOption configures tool adaptation behavior.
type AdaptOption func(*adaptConfig)

type adaptConfig struct {
	askFn           AskUserFunc
	messagesGetter  MessagesGetter
	activityFn      func(toolCallID string, msg string)
	promptResponder BashPromptResponderProvider
	sideEffectFn    func(string, any)
}

// WithAskUser sets the handler the AskUserQuestion tool uses to ask the user.
func WithAskUser(fn AskUserFunc) AdaptOption {
	return func(c *adaptConfig) { c.askFn = fn }
}

// WithMessagesGetterProvider provides the current parent conversation to tools that
// need it, such as Agent when fork=true.
func WithMessagesGetterProvider(fn MessagesGetter) AdaptOption {
	return func(c *adaptConfig) { c.messagesGetter = fn }
}

// WithToolActivity sets the handler for activity lines emitted by agent-like tools.
func WithToolActivity(fn func(toolCallID string, msg string)) AdaptOption {
	return func(c *adaptConfig) { c.activityFn = fn }
}

// WithBashPromptResponderProvider sets the responder provider for tools that can
// safely handle interactive prompts during execution.
func WithBashPromptResponderProvider(fn BashPromptResponderProvider) AdaptOption {
	return func(c *adaptConfig) { c.promptResponder = fn }
}

// WithSideEffectHandler routes handoffs to their owning run instead of the
// default main-agent store. Tool call IDs are not unique across conversations.
func WithSideEffectHandler(fn func(string, any)) AdaptOption {
	return func(c *adaptConfig) { c.sideEffectFn = fn }
}

// AdaptTool wraps a legacy Tool as a core.Tool with a dynamic CWD resolver.
func AdaptTool(t Tool, schema core.ToolSchema, cwd func() string, opts ...AdaptOption) core.Tool {
	return adaptTool(t, schema, cwd, adaptationConfig(opts))
}

func adaptationConfig(opts []AdaptOption) adaptConfig {
	var cfg adaptConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

func adaptTool(t Tool, schema core.ToolSchema, cwd func() string, cfg adaptConfig) *toolAdapter {
	return &toolAdapter{inner: t, schema: schema, cwd: cwd, askFn: cfg.askFn, messagesGetter: cfg.messagesGetter, activityFn: cfg.activityFn, promptResponder: cfg.promptResponder, sideEffectFn: cfg.sideEffectFn}
}

// AdaptToolRegistry wraps all tools from the global registry as core.Tools.
// The schema list maps tool names to their JSON schemas.
func AdaptToolRegistry(schemas []core.ToolSchema, cwd func() string, opts ...AdaptOption) *core.Tools {
	cfg := adaptationConfig(opts)

	schemaByName := make(map[string]core.ToolSchema, len(schemas))
	for _, s := range schemas {
		if s.Name != "" {
			schemaByName[s.Name] = s
		}
	}

	var adapted []core.Tool
	for name, schema := range schemaByName {
		if t, ok := Get(name); ok {
			adapted = append(adapted, adaptTool(t, schema, cwd, cfg))
		}
	}
	return core.NewTools(adapted...)
}

// toolAdapter wraps a legacy Tool as a core.Tool.
type toolAdapter struct {
	inner           Tool
	schema          core.ToolSchema
	cwd             func() string
	askFn           AskUserFunc
	messagesGetter  MessagesGetter
	activityFn      func(toolCallID string, msg string)
	promptResponder BashPromptResponderProvider
	sideEffectFn    func(string, any)
}

func (a *toolAdapter) Schema() core.ToolSchema { return a.schema }

func (a *toolAdapter) Run(ctx context.Context, call ai.ToolCall) (agent.Result, error) {
	input, _ := core.ParseToolInput(call.Input)
	cwd := ""
	if a.cwd != nil {
		cwd = a.cwd()
	}
	if a.messagesGetter != nil {
		ctx = WithMessagesGetter(ctx, a.messagesGetter)
	}
	if a.promptResponder != nil {
		ctx = ContextWithBashPromptResponderProvider(ctx, a.promptResponder)
	}
	// Shell output uses the SDK's display-only partial results, not activity
	// strings: the rounded counter must not become the source of row counts.
	if IsShellTool(a.inner.Name()) {
		inherited := BashProgressFromContext(ctx)
		reportCtx := ctx
		ctx = ContextWithBashProgress(ctx, func(lines int, bytes int64, tail string) {
			agent.Report(reportCtx, agent.Result{
				Content: ai.TextContent(formatBashProgress(lines, bytes)),
				Details: toolresult.BashDetails{LineCount: lines, Tail: tail, TailTruncated: bytes > int64(len(tail))},
			})
			if inherited != nil {
				inherited(lines, bytes, tail)
			}
		})
	}
	if IsAgentToolName(a.inner.Name()) {
		if a.activityFn != nil {
			if callID := core.ToolCallIDFromContext(ctx); callID != "" {
				input["_onActivity"] = ActivityFunc(func(msg string) {
					a.activityFn(callID, msg)
				})
			}
		}
		// Forward the interaction channel so foreground subagents can route
		// AskUserQuestion prompts to the user through their spawning turn.
		if a.askFn != nil {
			input["_onQuestion"] = AskQuestionFunc(a.askFn)
		}
	}

	var result toolresult.ToolResult
	if it, ok := a.inner.(InteractiveTool); ok && it.RequiresInteraction() && a.askFn != nil {
		request, err := it.PrepareInteraction(ctx, input, cwd)
		if err != nil {
			return agent.Result{}, err
		}
		qr, ok := request.(*QuestionRequest)
		if !ok {
			return agent.Result{}, fmt.Errorf("unexpected interaction type")
		}
		resp, err := a.askFn(ctx, qr)
		if err != nil {
			return agent.Result{}, err
		}
		result = it.ExecuteWithResponse(ctx, input, resp, cwd)
	} else {
		result = a.inner.Execute(ctx, input, cwd)
	}

	if callID := core.ToolCallIDFromContext(ctx); callID != "" && result.HookResponse != nil {
		if a.sideEffectFn != nil {
			a.sideEffectFn(callID, result.HookResponse)
		} else {
			sideEffects.Store(callID, result.HookResponse)
		}
	}

	// Details ride on the result, where the interface and the transcript
	// recorder both read them off ToolEnd; the model is told only the text.
	text := result.FormatForLLM()
	res := agent.TextResult(text)
	res.Details = result.Details
	if !result.Success {
		// Keep full output in Content, and use only the reason in error notices.
		if result.Error != "" {
			return res, fmt.Errorf("%s", result.Error)
		}
		return res, fmt.Errorf("%s", text)
	}
	return res, nil
}
