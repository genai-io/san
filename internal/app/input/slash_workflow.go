package input

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

// IsWorkflowStopCommand identifies the one slash command that must bypass the
// foreground turn's input queue: waiting for that turn would delay cancellation.
func IsWorkflowStopCommand(input string) bool {
	name, args, ok := command.ParseCommand(input)
	if !ok || name != "workflow" {
		return false
	}
	fields := strings.Fields(args)
	return len(fields) > 0 && fields[0] == "stop"
}

// workflowRunner is what /workflow needs from the Workflow tool.
type workflowRunner interface {
	Saved() string
	Preview(name string, width int) (string, error)
	Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
}

// handleWorkflowCommand runs a saved workflow directly. The user has already
// named what to run, so there is nothing for a model turn to decide — routing
// it through one would only add latency and a chance to get the call wrong.
// Launch applies the same checks and starts the same background task as a
// model's call; and because a tool the model is not offered is still
// registered, this works with the tool disabled.
func (c *SlashCommandController) handleWorkflowCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	fields, err := command.SplitArguments(args)
	if err != nil {
		return "", nil, err
	}
	if len(fields) > 0 && fields[0] == "stop" {
		return c.stopWorkflow(fields[1:])
	}
	if c.env.ToolSvc == nil {
		return "", nil, errors.New("the Workflow tool is not available in this build")
	}
	t, _ := c.env.ToolSvc.Get(tool.ToolWorkflow)
	wt, ok := t.(workflowRunner)
	if !ok {
		return "", nil, errors.New("the Workflow tool is not available in this build")
	}
	if len(fields) == 0 || fields[0] == "list" {
		if len(fields) > 1 {
			return "", nil, errors.New("usage: /workflow list")
		}
		list := wt.Saved()
		if list == "" {
			return "No saved workflows. Add one as .san/workflows/<name>.md.\n\n" + workflowUsage, nil, nil
		}
		return "Saved workflows:" + list + "\n\n" + workflowUsage, nil, nil
	}
	if fields[0] == "show" {
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow show <name>")
		}
		preview, err := wt.Preview(fields[1], c.env.Width)
		return preview, nil, err
	}

	name, inputFields := fields[0], fields[1:]
	if name == "run" {
		if len(inputFields) == 0 {
			return "", nil, errors.New("usage: /workflow run <name> [key=value …]")
		}
		name, inputFields = inputFields[0], inputFields[1:]
	}
	inputs := map[string]string{}
	for _, kv := range inputFields {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	bounds, id, err := wt.Launch(name, inputs, c.env.Width)
	if err != nil {
		return "", nil, err
	}
	// A direct slash launch has no model stream to start the frame clock.
	// Keep the pinned graph repainting while its background task runs.
	var tick tea.Cmd
	if c.env.SpinnerTickCmd != nil {
		tick = c.env.SpinnerTickCmd()
	}
	return bounds + "\nTask ID: " + id + "\nLive activity: task area (Alt+T) · Stop: Ctrl+C (when idle) or /workflow stop " + id, tick, nil
}

func (c *SlashCommandController) stopWorkflow(args []string) (string, tea.Cmd, error) {
	if len(args) > 1 {
		return "", nil, errors.New("usage: /workflow stop [task-id]")
	}
	if c.env.TaskSvc == nil {
		return "", nil, errors.New("task manager is unavailable")
	}
	var target task.BackgroundTask
	if len(args) == 1 {
		var ok bool
		target, ok = c.env.TaskSvc.Get(args[0])
		if !ok || target.GetStatus().AgentName != "workflow" {
			return "", nil, fmt.Errorf("workflow task %s not found", args[0])
		}
		if !target.IsRunning() {
			return "", nil, fmt.Errorf("workflow task %s has already finished", args[0])
		}
	} else {
		var running []task.BackgroundTask
		for _, bg := range c.env.TaskSvc.ListRunning() {
			if bg.GetStatus().AgentName == "workflow" {
				running = append(running, bg)
			}
		}
		switch len(running) {
		case 0:
			return "No running workflows.", nil, nil
		case 1:
			target = running[0]
		default:
			ids := make([]string, 0, len(running))
			for _, bg := range running {
				ids = append(ids, bg.GetID())
			}
			sort.Strings(ids)
			return "Several workflows are running. Choose one: /workflow stop <task-id>\n  " + strings.Join(ids, "\n  "), nil, nil
		}
	}
	if err := target.Stop(); err != nil {
		return "", nil, err
	}
	return "Stopping workflow " + target.GetID() + ".", nil, nil
}

const workflowUsage = "Commands:\n  /workflow list                         List saved workflows\n  /workflow show <name>                  Preview a graph\n  /workflow run <name> [key=value …]     Run a workflow\n  /workflow stop [task-id]               Stop a running workflow"
