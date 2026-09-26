package input

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/tool"
)

// workflowRunner is what /workflow needs from the Workflow tool.
type workflowRunner interface {
	Saved() string
	Launch(name string, inputs map[string]string) (bounds, taskID string, err error)
}

// handleWorkflowCommand runs a saved workflow directly. The user has already
// named what to run, so there is nothing for a model turn to decide — routing
// it through one would only add latency and a chance to get the call wrong.
// Launch applies the same checks and starts the same background task as a
// model's call; and because a tool the model is not offered is still
// registered, this works with the tool disabled.
func (c *SlashCommandController) handleWorkflowCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	t, _ := c.env.ToolSvc.Get(tool.ToolWorkflow)
	wt, ok := t.(workflowRunner)
	if !ok {
		return "", nil, errors.New("the Workflow tool is not available in this build")
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		list := wt.Saved()
		if list == "" {
			return "No saved workflows. Add one as .san/workflows/<name>.md.", nil, nil
		}
		return "Saved workflows:" + list + "\n\nRun one with /workflow <name> [key=value …].", nil, nil
	}

	// ponytail: whitespace-split key=value, so an input value cannot contain a
	// space; quote-aware parsing if a real definition needs one.
	inputs := map[string]string{}
	for _, kv := range fields[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	bounds, id, err := wt.Launch(fields[0], inputs)
	if err != nil {
		return "", nil, err
	}
	return bounds + "\nTask ID: " + id, nil, nil
}
