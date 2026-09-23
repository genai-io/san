// Package workflow exposes the Workflow tool: a markdown-defined graph of
// subagent turns, validated at approval time and run as one background task.
// Parent-only, like Agent — main launches workflows, workers do not.
package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/san/internal/workflow"
)

// WorkflowTool runs a workflow definition through the subagent executor.
type WorkflowTool struct {
	executor tool.AgentExecutor
	// dirs are the saved-workflow directories, in priority order. Rewired
	// alongside executor whenever the app reconfigures the tool.
	dirs []string
}

// NewWorkflowTool creates a WorkflowTool without an executor; the app wires
// one in through SetExecutor, as it does for Agent.
func NewWorkflowTool() *WorkflowTool { return &WorkflowTool{} }

func (t *WorkflowTool) Name() string { return tool.ToolWorkflow }
func (t *WorkflowTool) Description() string {
	return "Run a graph of subagents defined in markdown"
}
func (t *WorkflowTool) Icon() string             { return tool.IconAgent }
func (t *WorkflowTool) RequiresPermission() bool { return true }

// SetExecutor sets the subagent executor every node runs through.
func (t *WorkflowTool) SetExecutor(executor tool.AgentExecutor) { t.executor = executor }

// SetSearchPaths sets where saved workflows are looked up, highest priority
// first. The app passes the project directory before the user one, matching
// how subagent definitions resolve.
func (t *WorkflowTool) SetSearchPaths(dirs []string) { t.dirs = dirs }

// saved lists the definitions on disk for the schema description, so the
// model can name one instead of rewriting it.
func (t *WorkflowTool) saved() string {
	entries := workflow.Load(t.dirs...)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range entries {
		b.WriteString("\n- " + s.Name)
		if s.Description != "" {
			b.WriteString(" — " + s.Description)
		}
	}
	return b.String()
}

func (t *WorkflowTool) Schema() core.ToolSchema {
	var b strings.Builder
	b.WriteString("Run a workflow: several subagent turns arranged as a graph, in the background, reporting one summary when done. " +
		"Use it only when the same arrangement is worth keeping — a dependency order a long turn could forget, or a fan-out whose intermediate results should stay out of this conversation. " +
		"For one bounded job, or a one-off fan-out, use Agent instead.\n\n")
	if list := t.saved(); list != "" {
		b.WriteString("Saved workflows — pass one of these as `name`:" + list + "\n\n")
	}
	b.WriteString("Otherwise pass `definition`: a markdown document. Optional frontmatter sets name and max_parallel (default 4). " +
		"One ```mermaid block gives the topology: `a --> b` runs b after a with a's output; `a --> b & c` fans out; `a -->|HIGH| b` runs b only when a's trimmed output equals HIGH. " +
		"Each node is a `## id` section: optional leading `agent:`, `mode:` (explore|edit|default), `model:`, `continue_on_error:` lines, then the prompt. " +
		"`{{id}}` inserts the output of any node upstream on a taken path; `{{input.key}}` inserts a value from inputs.\n\n" +
		"A node with `for_each: plan.tasks` and `max_workers: N` runs once per item of the JSON array its upstream returned, up to N — `{{item}}` is the item, `{{item.key}}` one of its fields. " +
		"A plan with more items than max_workers is refused, so state a limit in the planning node's prompt too.\n\n" +
		"A conditional edge pointing back at an ancestor is a bounded retry and must carry its bound: `review -->|FAIL x3| draft` redoes draft/review up to three times, escaping through `review -->|PASS| ship`. " +
		"Inside a round, `{{review}}` is the previous round's output, empty on the first; using every round without escaping fails the workflow.\n\n" +
		"Every node is a fresh subagent — brief each one fully. Validation errors come back before anything runs.")

	return core.ToolSchema{
		Name:        t.Name(),
		Description: b.String(),
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "A saved workflow to run. Mutually exclusive with definition",
				},
				"definition": map[string]any{
					"type":        "string",
					"description": "The workflow document: frontmatter, one mermaid flowchart, one `## id` section per node",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "One line on what this run is for (shown to the user)",
				},
				"inputs": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Values for {{input.key}} references",
				},
			},
		},
	}
}

// resolve returns the workflow the call names: a saved one by name, or the
// inline definition. Exactly one of the two.
func (t *WorkflowTool) resolve(params map[string]any) (*workflow.Workflow, error) {
	name := strings.TrimSpace(tool.GetString(params, "name"))
	definition := strings.TrimSpace(tool.GetString(params, "definition"))
	switch {
	case name != "" && definition != "":
		return nil, fmt.Errorf("pass name or definition, not both")
	case name != "":
		w, err := workflow.Find(name, t.dirs...)
		switch {
		case errors.Is(err, workflow.ErrNotFound):
			return nil, fmt.Errorf("%w. Saved workflows:%s", err, cmp.Or(t.saved(), " (none saved)"))
		case err != nil:
			return nil, fmt.Errorf("workflow %s is invalid:\n%w", name, err)
		}
		return w, nil
	case definition != "":
		w, err := workflow.Parse(definition)
		if err != nil {
			return nil, fmt.Errorf("invalid workflow:\n%w", err)
		}
		return w, nil
	default:
		return nil, fmt.Errorf("name or definition is required")
	}
}

// PreparePermission resolves the workflow and validates every node's agent
// before the user is asked, so a workflow that cannot run is refused rather
// than approved. Nothing is carried over to Execute: every stage of a tool
// call parses its own copy of the arguments, so a value stashed on this map
// would be dropped on the floor.
func (t *WorkflowTool) PreparePermission(_ context.Context, params map[string]any, _ string) (*perm.PermissionRequest, error) {
	if t.executor == nil {
		return nil, fmt.Errorf("agent executor not configured")
	}
	w, err := t.resolve(params)
	if err != nil {
		return nil, err
	}
	for _, n := range w.Nodes {
		agent := n.Config["agent"]
		info, _, ok := t.executor.ResolveAgentSelection(agent)
		if !ok {
			return nil, fmt.Errorf("node %s: agent %q is disabled", n.ID, agent)
		}
		// An unknown name resolves to a display label rather than an error,
		// which is right for an Agent call the model just named on the spot.
		// A workflow is the opposite case: the name was written down, so the
		// only way it fails to resolve is a typo, and nobody is watching the
		// run to notice that the node quietly got the default agent instead.
		// Every registered definition carries where it was loaded from.
		if agent != "" && info.Source == "" {
			return nil, fmt.Errorf("node %s: no agent named %q — drop the line for the default agent, or set mode: explore|edit for a read-only or editing node", n.ID, agent)
		}
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: describe(w, tool.GetString(params, "description")),
	}, nil
}

// describe is the approval title: what runs, and the worst case it can reach.
// The approval view has no workflow preview yet, so this is what the user
// approves; the definition itself is in the tool_use block.
func describe(w *workflow.Workflow, description string) string {
	name := w.Name
	if name == "" {
		name = "(unnamed)"
	}
	if description == "" {
		description = w.Description
	}
	turns, fanOut := w.Bounds()
	s := fmt.Sprintf("Run workflow %s: %d nodes, up to %d subagent turns, max %d parallel", name, len(w.Nodes), turns, w.MaxParallel)
	if description != "" {
		s += " — " + description
	}
	for _, line := range fanOut {
		s += "\n" + line
	}
	return s
}

func (t *WorkflowTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.Execute(ctx, params, cwd)
}

// Execute launches the run as one background task and returns at once; the
// summary arrives as that task's completion notification.
func (t *WorkflowTool) Execute(_ context.Context, params map[string]any, _ string) toolresult.ToolResult {
	if t.executor == nil {
		return toolresult.NewErrorResult(t.Name(), "agent executor not configured")
	}
	w, err := t.resolve(params)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	inputs := map[string]string{}
	if raw, ok := params["inputs"].(map[string]any); ok {
		for k, v := range raw {
			inputs[k] = fmt.Sprint(v)
		}
	}

	description := tool.GetString(params, "description")
	if description == "" {
		description = "Run workflow " + w.Name
	}
	ctx, cancel := context.WithCancel(context.Background())
	bg := task.Default().CreateAgentTask(task.NewID(), "workflow", description, ctx, cancel)

	go func() {
		defer cancel()
		runner := &nodeRunner{exec: t.executor, task: bg, name: w.Name}
		res := workflow.Run(ctx, w, runner, workflow.Options{
			Inputs: inputs,
			OnStatus: func(n *workflow.Node, s workflow.Status) {
				bg.AppendProgress(n.ID + ": " + string(s))
			},
		})
		bg.AppendOutput([]byte(res.Summary(w)))
		switch {
		case ctx.Err() != nil:
			bg.Complete(ctx.Err())
		case res.Failed(w):
			// The notification shows the error in place of the output, so the
			// verdict carries what failed; the full summary stays in the task.
			bg.Complete(fmt.Errorf("workflow %s failed — %s", w.Name, strings.Join(res.Failures(w), "; ")))
		default:
			bg.Complete(nil)
		}
	}()

	return toolresult.ToolResult{
		Success: true,
		Output: fmt.Sprintf("Workflow %s started in background.\nTask ID: %s\nNodes: %d"+tool.BackgroundLaunchSuffix,
			w.Name, bg.GetID(), len(w.Nodes)),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("[background] %s: %s", w.Name, bg.GetID()),
		},
	}
}

// nodeRunner is the workflow.NodeRunner seam: one node is one subagent turn
// through the same executor, and so the same permission gate, as Agent.
type nodeRunner struct {
	exec   tool.AgentExecutor
	task   *task.AgentTask
	name   string
	steps  atomic.Int64
	tokens atomic.Int64
}

func (r *nodeRunner) RunNode(ctx context.Context, c workflow.Call) (string, error) {
	n := c.Node
	label := n.ID
	if c.Label != "" {
		label += "·" + c.Label
	}
	res, err := r.exec.Run(ctx, tool.AgentExecRequest{
		Agent:       n.Config["agent"],
		Prompt:      c.Prompt,
		Description: r.name + "/" + label,
		Background:  true,
		Model:       n.Config["model"],
		Mode:        n.Config["mode"],
		OnActivity:  func(msg string) { r.task.AppendProgress(label + " ▸ " + msg) },
	})
	if err != nil {
		return "", err
	}
	steps := r.steps.Add(int64(res.StepCount))
	tokens := r.tokens.Add(int64(res.TotalInputTokens + res.TotalOutputTokens))
	r.task.UpdateProgress(int(steps), int(tokens))
	if !res.Success {
		return res.Content, fmt.Errorf("%s", res.Error)
	}
	return res.Content, nil
}

func init() {
	tool.Register(NewWorkflowTool())
}
