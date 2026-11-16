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

// IsWorkflowStopCommand keeps cancellation out of the foreground input queue.
func IsWorkflowStopCommand(input string) bool {
	name, args, ok := command.ParseCommand(input)
	if !ok || name != "workflow" {
		return false
	}
	fields := strings.Fields(args)
	return len(fields) > 0 && fields[0] == "stop"
}

type workflowRunner interface {
	Saved() string
	Preview(name string, width int) (string, error)
	Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
}

// Management is explicit and needs no model turn or ambient authoring prompt.
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
	if len(fields) == 0 && c.env.Input != nil {
		if svc, ok := t.(WorkflowService); ok {
			return "", c.env.Input.Workflow.Open(svc, c.env.Width, c.env.Height), nil
		}
	}
	if len(fields) == 0 || fields[0] == "list" {
		if len(fields) > 1 {
			return "", nil, errors.New("usage: /workflow list")
		}
		if list := wt.Saved(); list != "" {
			return "Saved workflows:" + list + "\n\n" + workflowUsage, nil, nil
		}
		return "No saved workflows. Create one with /workflow new [name].\n\n" + workflowUsage, nil, nil
	}
	if fields[0] == "show" {
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow show <name>")
		}
		preview, err := wt.Preview(fields[1], c.env.Width)
		return preview, nil, err
	}
	if fields[0] == "new" || fields[0] == "from-skill" || fields[0] == "edit" || fields[0] == "inspect" {
		svc, ok := t.(WorkflowService)
		if !ok || c.env.Input == nil {
			return "", nil, errors.New("workflow management is unavailable in this build")
		}
		return c.openWorkflow(svc, fields)
	}
	if fields[0] == "run" {
		if len(fields) < 2 {
			return "", nil, errors.New("usage: /workflow run <name> [key=value …]")
		}
		fields = fields[1:]
	}
	inputs := map[string]string{}
	for _, kv := range fields[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	bounds, id, err := wt.Launch(fields[0], inputs, c.env.Width)
	if err != nil {
		return "", nil, err
	}
	var cmds []tea.Cmd
	if c.env.SpinnerTickCmd != nil {
		cmds = append(cmds, c.env.SpinnerTickCmd())
	}
	if c.env.Input != nil {
		if svc, ok := t.(WorkflowService); ok {
			cmds = append(cmds, c.env.Input.Workflow.OpenTask(svc, id, c.env.Width, c.env.Height))
		}
	}
	notice := bounds + "\nTask ID: " + id
	notice += "\nLive activity: task area (Alt+T) · Stop: Ctrl+C (when idle) or /workflow stop " + id
	return notice, tea.Batch(cmds...), nil
}

func (c *SlashCommandController) openWorkflow(svc WorkflowService, fields []string) (string, tea.Cmd, error) {
	view := &c.env.Input.Workflow
	w, h := c.env.Width, c.env.Height
	switch fields[0] {
	case "new":
		if len(fields) > 2 {
			return "", nil, errors.New("usage: /workflow new [name]")
		}
		name := "new-workflow"
		if len(fields) == 2 {
			name = fields[1]
		}
		src := workflowDraft(name, "work --> review", `## work
mode: explore

Complete this task and report your findings:
{{input.task}}

## review
mode: explore

Check these findings against the task and produce the final result.
Task: {{input.task}}
Findings: {{work}}
`)
		return "", view.OpenDraft(svc, name, src, "", w, h), nil
	case "from-skill":
		if len(fields) < 2 || len(fields) > 3 {
			return "", nil, errors.New("usage: /workflow from-skill <skill> [name]")
		}
		if c.env.Skill == nil {
			return "", nil, errors.New("skill registry is unavailable")
		}
		sk, ok := c.env.Skill.Get(fields[1])
		if !ok || !sk.IsEnabled() {
			return "", nil, fmt.Errorf("skill %q is missing or disabled", fields[1])
		}
		name := strings.ReplaceAll(sk.FullName(), ":", "-")
		if len(fields) == 3 {
			name = fields[2]
		}
		// Let the Skill loader retain its instructions and resource paths;
		// guessing a graph from prose could change the original behavior.
		prompt := fmt.Sprintf(`## work
mode: default

Use the Skill tool to invoke %q for this task:
{{input.task}}

Preserve the skill's original order, constraints and resource paths. If it requires a parent-only tool, report that limitation rather than bypassing it.

This workflow references the live skill; its instructions are loaded when the node runs. Review this before saving.
`, sk.FullName())
		return "", view.OpenDraft(svc, name, workflowDraft(name, "work", prompt), "", w, h), nil
	case "edit":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow edit <name>")
		}
		src, err := svc.Definition(fields[1])
		if err != nil {
			return "", nil, err
		}
		return "", view.OpenDraft(svc, fields[1], src, src, w, h), nil
	case "inspect":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow inspect <run-id>")
		}
		return "", view.OpenRun(svc, fields[1], w, h), nil
	}
	return "", nil, errors.New("unknown workflow action")
}

func workflowDraft(name, graph, sections string) string {
	return fmt.Sprintf("---\nname: %q\nmax_parallel: 4\n---\n\n```mermaid\nflowchart LR\n  %s\n```\n\n%s", name, graph, sections)
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

const workflowUsage = `Commands:
  /workflow new [name]                   Create a reviewable draft
  /workflow from-skill <skill> [name]    Create a Skill-derived draft
  /workflow edit <name>                  Review and edit a definition
  /workflow list                         List saved workflows
  /workflow show <name>                  Preview a graph
  /workflow run <name> [key=value …]     Run a workflow
  /workflow inspect <run-id>             Inspect or resume a run
  /workflow stop [task-id]               Stop a running workflow`
