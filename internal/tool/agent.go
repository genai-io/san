package tool

import (
	"context"
	"time"

	"github.com/genai-io/san/internal/core"
)

const (
	// IconAgent is the display icon for agent tool results.
	IconAgent = "a"
)

// messagesGetterKey is the context key for parent messages getter (used by fork).
type messagesGetterKey struct{}

// agentIDKey carries the broker address of the agent whose tools are running,
// so SendMessage can stamp the sender. Empty for the main conversation.
type agentIDKey struct{}

// WithAgentID marks the context with the running subagent's broker address.
func WithAgentID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, agentIDKey{}, id)
}

// AgentIDFromContext returns the running subagent's broker address, or "" when
// the caller is the main conversation.
func AgentIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(agentIDKey{}).(string); ok {
		return id
	}
	return ""
}

// WithMessagesGetter returns a context carrying a messages getter for fork support.
func WithMessagesGetter(ctx context.Context, getter MessagesGetter) context.Context {
	return context.WithValue(ctx, messagesGetterKey{}, getter)
}

// GetMessagesGetter returns the messages getter from context, if any.
func GetMessagesGetter(ctx context.Context) MessagesGetter {
	if g, ok := ctx.Value(messagesGetterKey{}).(MessagesGetter); ok {
		return g
	}
	return nil
}

// AgentExecutor is the interface for executing agents.
// This allows the Agent tool to be decoupled from the agent package.
type AgentExecutor interface {
	Run(ctx context.Context, req AgentExecRequest) (*AgentExecResult, error)
	RunBackground(req AgentExecRequest) (AgentTaskInfo, error)
	GetAgentConfig(name string) (AgentConfigInfo, bool)
	ResolveAgentRequest(name string) (AgentConfigInfo, any, bool)
	GetParentModelID() string
}

// ActivityFunc is called when the agent reports activity.
type ActivityFunc func(msg string)

// MessagesGetter returns the current parent conversation messages.
// Used by fork to inherit conversation context.
type MessagesGetter func() []core.Message

// AgentExecRequest contains parameters for agent execution.
type AgentExecRequest struct {
	Agent       string
	Config      any // internal resolved configuration; never model-facing
	Prompt      string
	Description string
	Background  bool
	Model       string
	MaxSteps    int
	Mode        string
	// TaskID is the background-task id of this run; the executor registers it
	// with the broker so main can message the subagent while it runs. Empty
	// for foreground runs.
	TaskID     string
	OnActivity ActivityFunc
	OnQuestion AskQuestionFunc
}

// AgentExecResult contains the result of agent execution.
type AgentExecResult struct {
	AgentID           string
	AgentName         string
	OutputFile        string
	Model             string
	Success           bool
	Content           string
	StepCount         int
	ToolUses          int
	TotalInputTokens  int
	TotalOutputTokens int
	Duration          time.Duration
	Activity          []string
	Error             string
}

// AgentTaskInfo contains info about a background agent task.
type AgentTaskInfo struct {
	TaskID     string
	AgentName  string
	OutputFile string
}

// AgentConfigInfo contains agent configuration for display. It is the single
// projection of an agent definition, shared by the Agent tool (GetAgentConfig)
// and the TUI agent selector.
type AgentConfigInfo struct {
	Name           string
	Description    string
	Color          string
	Model          string
	PermissionMode string
	Tools          []string // nil = all tools
	SourceFile     string
	// Source indicates where the custom agent definition came from:
	// "user", "project", or a plugin scope. Empty defaults to project.
	Source string
}
