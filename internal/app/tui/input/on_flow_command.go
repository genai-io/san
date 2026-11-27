package input

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

// IsFlowStopCommand keeps cancellation out of the foreground input queue.
func IsFlowStopCommand(input string) bool {
	name, args, ok := command.ParseCommand(input)
	if !ok || name != "flow" {
		return false
	}
	fields, err := command.SplitArguments(args)
	return err == nil && len(fields) > 0 && fields[0] == "stop" && isFlowSubcommand(fields, nil)
}

// isFlowSubcommand reports whether fields have the exact shape of a /flow
// subcommand. Anything else, such as "run the tests and fix failures", is a
// natural-language goal even when it starts with a subcommand word. A run of a
// saved flow stays a command so a malformed input is reported, not drafted.
func isFlowSubcommand(fields []string, saved func(string) bool) bool {
	n := len(fields)
	if n == 0 {
		return true
	}
	switch fields[0] {
	case "list":
		return n == 1
	case "show", "edit", "inspect":
		return n == 2
	case "stop", "new":
		return n <= 2
	case "from-skill":
		return n == 2 || n == 3
	case "run":
		if n < 2 || saved != nil && saved(fields[1]) {
			return true
		}
		for _, kv := range fields[2:] {
			if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
				return false
			}
		}
		return true
	}
	return false
}

// Authoring is loaded only for an explicit natural-language /flow request.
func (c *SlashCommandController) handleFlowCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	args = strings.TrimSpace(args)
	fields, err := command.SplitArguments(args)
	if err != nil || !isFlowSubcommand(fields, c.savedFlow) {
		if c.env.Flow == nil || c.env.Input == nil {
			return "", nil, errors.New("flow authoring is unavailable in this build")
		}
		return "", c.env.Input.Flow.OpenGeneration(c.env.Flow, args, c.env.Width, c.env.Height), nil
	}
	if len(fields) > 0 && fields[0] == "stop" {
		return c.stopFlow(fields[1:])
	}
	svc := c.env.Flow
	if svc == nil {
		return "", nil, errors.New("flow workspace is unavailable in this build")
	}
	if len(fields) == 0 && c.env.Input != nil {
		return "", c.env.Input.Flow.Open(svc, c.env.Width, c.env.Height), nil
	}
	if len(fields) == 0 || fields[0] == "list" {
		if len(fields) > 1 {
			return "", nil, errors.New("usage: /flow list")
		}
		if list := savedFlowList(svc.SavedDefinitions()); list != "" {
			return "Saved flows:" + list + "\n\n" + flowUsage, nil, nil
		}
		return "No saved flows. Describe a goal with /flow <goal>.\n\n" + flowUsage, nil, nil
	}
	if fields[0] == "show" {
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /flow show <name>")
		}
		preview, err := svc.Preview(fields[1], c.env.Width)
		return preview, nil, err
	}
	if fields[0] == "new" || fields[0] == "from-skill" || fields[0] == "edit" || fields[0] == "inspect" {
		if c.env.Input == nil {
			return "", nil, errors.New("flow management is unavailable in this build")
		}
		return c.openFlow(svc, fields)
	}
	if len(fields) < 2 {
		return "", nil, errors.New("usage: /flow run <name> [key=value …]")
	}
	inputs := map[string]string{}
	for _, kv := range fields[2:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	if c.env.Input != nil {
		keys, err := svc.SavedInputNames(fields[1])
		if err != nil {
			return "", nil, err
		}
		if slices.ContainsFunc(keys, func(key string) bool { return strings.TrimSpace(inputs[key]) == "" }) {
			source, err := svc.Definition(fields[1])
			if err != nil {
				return "", nil, err
			}
			return "", c.env.Input.Flow.OpenRunInputs(svc, fields[1], source, inputs, c.env.Width, c.env.Height), nil
		}
	}
	if _, err := svc.Launch(fields[1], inputs, c.env.Width); err != nil {
		return "", nil, err
	}
	var cmds []tea.Cmd
	if c.env.SpinnerTickCmd != nil {
		cmds = append(cmds, c.env.SpinnerTickCmd())
	}
	return "", tea.Batch(cmds...), nil
}

// savedFlowList renders one "- name — description" line per definition.
func savedFlowList(saved []flow.SavedDefinition) string {
	var b strings.Builder
	for _, d := range saved {
		b.WriteString("\n- " + d.Name)
		if description := strings.TrimRight(d.Description, "\r\n"); description != "" {
			b.WriteString(" — " + description)
		}
	}
	return b.String()
}

func (c *SlashCommandController) savedFlow(name string) bool {
	if c.env.Flow == nil {
		return false
	}
	for _, saved := range c.env.Flow.SavedDefinitions() {
		if saved.Name == name {
			return true
		}
	}
	return false
}

func (c *SlashCommandController) openFlow(svc FlowWorkspace, fields []string) (string, tea.Cmd, error) {
	view := &c.env.Input.Flow
	w, h := c.env.Width, c.env.Height
	switch fields[0] {
	case "new":
		if len(fields) > 2 {
			return "", nil, errors.New("usage: /flow new [name]")
		}
		name := "new-flow"
		if len(fields) == 2 {
			name = fields[1]
		}
		src := flowDraft(name, "work", `## work
mode: explore

Complete the request below and return the result:
{{input}}
`)
		return "", view.OpenDraft(svc, name, src, "", w, h), nil
	case "from-skill":
		if len(fields) < 2 || len(fields) > 3 {
			return "", nil, errors.New("usage: /flow from-skill <skill> [name]")
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

Use the Skill tool to invoke %q for this request:
{{input}}

Preserve the skill's original order, constraints and resource paths. If it requires a parent-only tool, report that limitation rather than bypassing it.

This flow references the live skill; its instructions are loaded when the node runs. Review this before saving.
`, sk.FullName())
		return "", view.OpenDraft(svc, name, flowDraft(name, "work", prompt), "", w, h), nil
	case "edit":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /flow edit <name>")
		}
		src, err := svc.Definition(fields[1])
		if err != nil {
			return "", nil, err
		}
		return "", view.OpenDraft(svc, fields[1], src, src, w, h), nil
	case "inspect":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /flow inspect <run-id>")
		}
		return "", view.OpenRun(svc, fields[1], w, h), nil
	}
	return "", nil, errors.New("unknown flow action")
}

func flowDraft(name, graph, sections string) string {
	return fmt.Sprintf("---\nname: %q\nmax_parallel: 1\n---\n\n```mermaid\nflowchart LR\n  %s\n```\n\n%s", name, graph, sections)
}

func (c *SlashCommandController) stopFlow(args []string) (string, tea.Cmd, error) {
	if c.env.TaskSvc == nil {
		return "", nil, errors.New("task manager is unavailable")
	}
	if len(args) == 1 {
		if target, ok := c.env.TaskSvc.Get(args[0]); ok && target.GetStatus().Scheduler {
			if !target.IsRunning() {
				return "", nil, fmt.Errorf("flow task %s has already finished", args[0])
			}
			if err := target.Stop(); err != nil {
				return "", nil, err
			}
			return "Stopping flow " + target.GetID() + ".", nil, nil
		}
		// A run ID, as shown at launch and in /flow, names the same execution.
		if c.env.Flow == nil {
			return "", nil, fmt.Errorf("running flow %s not found", args[0])
		}
		if err := c.env.Flow.StopRun(args[0]); err != nil {
			return "", nil, fmt.Errorf("running flow %s not found: %w", args[0], err)
		}
		return "Stopping flow run " + args[0] + ".", nil, nil
	}
	var running []task.BackgroundTask
	for _, bg := range c.env.TaskSvc.ListRunning() {
		if bg.GetStatus().Scheduler {
			running = append(running, bg)
		}
	}
	switch len(running) {
	case 0:
		return "No running flows.", nil, nil
	case 1:
		if err := running[0].Stop(); err != nil {
			return "", nil, err
		}
		return "Stopping flow " + running[0].GetID() + ".", nil, nil
	}
	ids := make([]string, 0, len(running))
	for _, bg := range running {
		ids = append(ids, bg.GetID())
	}
	sort.Strings(ids)
	return "Several flows are running. Choose one: /flow stop <task-id>\n  " + strings.Join(ids, "\n  "), nil, nil
}

const flowUsage = `Commands:
  /flow <goal>                       Generate a blueprint for review
  /flow new [name]                   Create a manual draft
  /flow from-skill <skill> [name]    Create a Skill-derived draft
  /flow edit <name>                  Review and edit a definition
  /flow list                         List saved flows
  /flow show <name>                  Preview a graph
  /flow run <name> [key=value …]     Run a flow
  /flow inspect <run-id|task-id>     Inspect or resume a run
  /flow stop [run-id|task-id]        Stop a running flow`
