package subagent

import (
	"context"

	"github.com/genai-io/san/internal/core"
)

// activityTools wraps core.Tools to call onExec before each tool execution.
type activityTools struct {
	inner  core.Tools
	onExec func(name string, params map[string]any)
}

func (a *activityTools) Get(name string) core.Tool {
	t := a.inner.Get(name)
	if t == nil {
		return nil
	}
	return &activityTool{inner: t, onExec: a.onExec}
}
func (a *activityTools) All() []core.Tool                      { return a.inner.All() }
func (a *activityTools) Add(t core.Tool, caller string)        { a.inner.Add(t, caller) }
func (a *activityTools) Remove(name, caller string)            { a.inner.Remove(name, caller) }
func (a *activityTools) Schemas() []core.ToolSchema            { return a.inner.Schemas() }
func (a *activityTools) SetObserver(fn func(core.ToolsChange)) { a.inner.SetObserver(fn) }

type activityTool struct {
	inner  core.Tool
	onExec func(name string, params map[string]any)
}

func (t *activityTool) Name() string            { return t.inner.Name() }
func (t *activityTool) Description() string     { return t.inner.Description() }
func (t *activityTool) Schema() core.ToolSchema { return t.inner.Schema() }
func (t *activityTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	t.onExec(t.inner.Name(), input)
	return t.inner.Execute(ctx, input)
}
