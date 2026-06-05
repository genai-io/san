package subagent

import (
	"context"

	"github.com/genai-io/san/v2/internal/core"
)

// progressTools wraps core.Tools to call onExec before each tool execution.
type progressTools struct {
	inner  core.Tools
	onExec func(name string, params map[string]any)
}

func (p *progressTools) Get(name string) core.Tool {
	t := p.inner.Get(name)
	if t == nil {
		return nil
	}
	return &progressTool{inner: t, onExec: p.onExec}
}
func (p *progressTools) All() []core.Tool                      { return p.inner.All() }
func (p *progressTools) Add(t core.Tool, caller string)        { p.inner.Add(t, caller) }
func (p *progressTools) Remove(name, caller string)            { p.inner.Remove(name, caller) }
func (p *progressTools) Schemas() []core.ToolSchema            { return p.inner.Schemas() }
func (p *progressTools) SetObserver(fn func(core.ToolsChange)) { p.inner.SetObserver(fn) }

type progressTool struct {
	inner  core.Tool
	onExec func(name string, params map[string]any)
}

func (t *progressTool) Name() string            { return t.inner.Name() }
func (t *progressTool) Description() string     { return t.inner.Description() }
func (t *progressTool) Schema() core.ToolSchema { return t.inner.Schema() }
func (t *progressTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	t.onExec(t.inner.Name(), input)
	return t.inner.Execute(ctx, input)
}
