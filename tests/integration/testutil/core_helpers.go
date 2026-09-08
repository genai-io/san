package testutil

import (
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// PermitAllPermission allows every tool call. Replaces the former
// perm.AsPermissionFunc(perm.PermitAll()) test fixture.
func PermitAllPermission() perm.PermissionFunc {
	return func(context.Context, string, map[string]any) (bool, string) { return true, "" }
}

// ReadOnlyPermission allows safe (read-only) tools and denies the rest.
func ReadOnlyPermission() perm.PermissionFunc {
	return func(_ context.Context, name string, _ map[string]any) (bool, string) {
		if perm.IsSafeTool(name) {
			return true, ""
		}
		return false, "read-only mode: " + name + " not permitted"
	}
}

// DenyAllPermission denies every tool call (including safe tools).
func DenyAllPermission() perm.PermissionFunc {
	return func(_ context.Context, name string, _ map[string]any) (bool, string) {
		return false, name + " denied"
	}
}

// queued is a model that answers with these responses, one per call, and says
// so once the queue is dry. A CompletionResponse is an ai.Response, so there is
// nothing to render: aitest.Replies walks the block sequence it already is.
func queued(responses []llm.CompletionResponse) *aitest.Driver {
	turns := make([]aitest.Turn, 0, len(responses))
	for _, r := range responses {
		turns = append(turns, aitest.Replies(r))
	}
	return aitest.New(turns...)
}

// stubClient wraps a driver as the per-turn client an agent is built on. The
// same client answers every turn: only a real endpoint varies its headers with
// what the turn sends.
func stubClient(d *aitest.Driver) func([]core.Message) (*ai.Client, error) {
	client := d.Client()
	return func([]core.Message) (*ai.Client, error) { return client, nil }
}

// NewTestAgent creates a core.Agent backed by a model with queued responses.
// All globally registered tools (including dynamically registered fakes) are included.
func NewTestAgent(t *testing.T, responses ...llm.CompletionResponse) (core.Agent, *aitest.Driver) {
	t.Helper()
	fakeLLM := queued(responses)
	cwd := t.TempDir()
	return core.NewAgent(core.Config{
		ID:     "test-agent",
		Client: stubClient(fakeLLM),
		System: core.NewSystem(),
		Tools:  buildAllRegisteredTools(cwd),

		MaxSteps: 100,
	}), fakeLLM
}

// buildAllRegisteredTools creates a core.Tools wrapping ALL tools in the global registry,
// including dynamically registered fake tools. Unlike AdaptToolRegistry which only finds
// tools that have schemas in GetToolSchemas(), this walks the entire registry directly.
func buildAllRegisteredTools(cwd string) core.Tools {
	var adapted []core.Tool
	for _, name := range tool.Default().List() {
		t, ok := tool.Get(name)
		if !ok {
			continue
		}
		// The tool's own name, not the registry's lookup key: List() lowercases
		// for matching, and the schema is what the model is told to call.
		schema := core.ToolSchema{Name: t.Name(), Description: t.Description()}
		adapted = append(adapted, tool.AdaptTool(t, schema, func() string { return cwd }))
	}
	return core.NewTools(adapted...)
}

// NewTestAgentWithPermission creates a core.Agent with a permission function wrapping tools.
func NewTestAgentWithPermission(t *testing.T, permFn perm.PermissionFunc, responses ...llm.CompletionResponse) (core.Agent, *aitest.Driver) {
	t.Helper()
	fakeLLM := queued(responses)
	cwd := t.TempDir()
	return core.NewAgent(core.Config{
		ID:       "test-agent",
		Client:   stubClient(fakeLLM),
		System:   core.NewSystem(),
		Tools:    buildAllRegisteredTools(cwd),
		Gate:     tool.Permission(permFn),
		MaxSteps: 100,
	}), fakeLLM
}

// NewTestAgentWithMaxSteps creates a core.Agent with a specific max steps limit.
func NewTestAgentWithMaxSteps(t *testing.T, maxSteps int, responses ...llm.CompletionResponse) (core.Agent, *aitest.Driver) {
	t.Helper()
	fakeLLM := queued(responses)
	cwd := t.TempDir()
	return core.NewAgent(core.Config{
		ID:     "test-agent",
		Client: stubClient(fakeLLM),
		System: core.NewSystem(),
		Tools:  buildAllRegisteredTools(cwd),

		MaxSteps: maxSteps,
	}), fakeLLM
}

// BuildTestTools adapts all globally registered tools into a core.Tools for use in tests.
func BuildTestTools(t *testing.T) core.Tools {
	t.Helper()
	return buildAllRegisteredTools(t.TempDir())
}

// RunAgent sends a prompt to the agent, drains its outbox, and returns the result.
// It sends SigStop after the first OnTurn event (single cycle).
func RunAgent(ctx context.Context, ag core.Agent, prompt string) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}

	var agentErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		agentErr = ag.Run(ctx)
	}()

	select {
	case ag.Inbox() <- core.Inbound{Msg: core.UserMessage(prompt, nil)}:
	case <-ctx.Done():
		<-done
		return core.Result{}, ctx.Err()
	}

	var result core.Result
	var hasResult bool
	for ev := range ag.Outbox() {
		if turn, ok := ev.(core.TurnEnded); ok {
			result = turn.Result
			hasResult = true
			select {
			case ag.Inbox() <- core.Inbound{Signal: core.SigStop}:
			case <-ctx.Done():
			}
		}
	}

	<-done

	if agentErr != nil && !hasResult {
		return result, agentErr
	}
	return result, nil
}
