package tool

import (
	"context"
	"time"

	"github.com/genai-io/gen-code/internal/core"
)

const (
	// IconAgent is the display icon for agent tool results.
	IconAgent = "a"

	// MaxAgentNestingDepth is the maximum allowed nesting depth for subagents.
	MaxAgentNestingDepth = 5
)

// agentDepthKey is the context key used to track agent nesting depth.
type agentDepthKey struct{}

// messagesGetterKey is the context key for parent messages getter (used by fork).
type messagesGetterKey struct{}

// GetAgentDepth returns the current agent nesting depth from context.
func GetAgentDepth(ctx context.Context) int {
	if d, ok := ctx.Value(agentDepthKey{}).(int); ok {
		return d
	}
	return 0
}

// WithAgentDepth returns a context with the given agent nesting depth.
func WithAgentDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, agentDepthKey{}, depth)
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
	GetAgentConfig(agentType string) (AgentConfigInfo, bool)
	GetParentModelID() string
}

// ProgressFunc is called when the agent makes progress.
type ProgressFunc func(msg string)

// MessagesGetter returns the current parent conversation messages.
// Used by fork to inherit conversation context.
type MessagesGetter func() []core.Message

// AgentExecRequest contains parameters for agent execution.
type AgentExecRequest struct {
	Agent       string
	Name        string
	Prompt      string
	Description string
	Background  bool
	Model       string
	MaxSteps    int
	Mode        string
	ResumeID    string
	Isolation   string
	OnProgress  ProgressFunc
	OnQuestion  AskQuestionFunc
}

// AgentExecResult contains the result of agent execution.
type AgentExecResult struct {
	AgentID     string
	AgentName   string
	OutputFile  string
	Model       string
	Success     bool
	Content     string
	StepCount   int
	ToolUses    int
	TotalTokens int
	Duration    time.Duration
	Progress    []string
	Error       string
}

// AgentTaskInfo contains info about a background agent task.
type AgentTaskInfo struct {
	TaskID     string
	AgentName  string
	OutputFile string
}

// AgentConfigInfo contains agent configuration for display.
type AgentConfigInfo struct {
	Name           string
	Description    string
	Color          string
	Model          string
	PermissionMode string
	Tools          []string
}
