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

func (p *activityTools) Get(name string) core.Tool {
	t := p.inner.Get(name)
	if t == nil {
		return nil
	}
	return &activityTool{inner: t, onExec: p.onExec}
}
func (p *activityTools) All() []core.Tool                      { return p.inner.All() }
func (p *activityTools) Add(t core.Tool, caller string)        { p.inner.Add(t, caller) }
func (p *activityTools) Remove(name, caller string)            { p.inner.Remove(name, caller) }
func (p *activityTools) Schemas() []core.ToolSchema            { return p.inner.Schemas() }
func (p *activityTools) SetObserver(fn func(core.ToolsChange)) { p.inner.SetObserver(fn) }

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
