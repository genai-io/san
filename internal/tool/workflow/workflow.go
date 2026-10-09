// Package workflow exposes the Workflow tool: a markdown-defined graph of
// subagent turns, validated at approval time and run as one background task.
// Parent-only, like Agent — main launches workflows, workers do not.
package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/x/ansi"

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

// Saved lists the definitions on disk, one "- name — description" per line:
// the schema description carries it so the model can name one instead of
// rewriting it, and /workflow shows it to the user.
func (t *WorkflowTool) Saved() string {
	entries := t.SavedDefinitions()
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

// SavedDefinitions exposes the same ordered names used by /workflow and the
// tool schema, so command completion follows the configured search paths.
func (t *WorkflowTool) SavedDefinitions() []workflow.Saved {
	return workflow.Load(t.dirs...)
}

// SavedInputNames returns the named definition's template inputs for command
// completion. The same parsed definition is used by preview and launch.
func (t *WorkflowTool) SavedInputNames(name string) ([]string, error) {
	w, err := workflow.Find(name, t.dirs...)
	if err != nil {
		return nil, err
	}
	return w.InputNames(), nil
}

func (t *WorkflowTool) Schema() core.ToolSchema {
	var b strings.Builder
	b.WriteString("Run a workflow: several subagent turns arranged as a graph, in the background, reporting one summary when done. " +
		"Use it only when the same arrangement is worth keeping — a dependency order a long turn could forget, or a fan-out whose intermediate results should stay out of this conversation. " +
		"For one bounded job, or a one-off fan-out, use Agent instead.\n\n")
	if list := t.Saved(); list != "" {
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
			return nil, fmt.Errorf("%w. Saved workflows:%s", err, cmp.Or(t.Saved(), " (none saved)"))
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

// prepare resolves the call's workflow and checks every node's agent. Every
// way in calls it, so a plan that cannot run is refused however it started;
// nothing carries between stages — each parses its own copy of the arguments.
func (t *WorkflowTool) prepare(params map[string]any) (*workflow.Workflow, error) {
	if t.executor == nil {
		return nil, errors.New("agent executor not configured")
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
	return w, nil
}

// PreparePermission shows the user what they are approving: the plan and the
// worst case it can reach.
func (t *WorkflowTool) PreparePermission(_ context.Context, params map[string]any, _ string) (*perm.PermissionRequest, error) {
	w, err := t.prepare(params)
	if err != nil {
		return nil, err
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

// Preview validates a saved workflow and shows its topology without running it.
func (t *WorkflowTool) Preview(name string, width int) (string, error) {
	w, err := t.prepare(map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	turns, fanOut := w.Bounds()
	preview := fmt.Sprintf("Preview only · up to %d turns · max %d parallel\n\n%s", turns, w.MaxParallel, w.CompactProgressView(nil, workflowDisplayWidth(width)))
	if w.Description != "" {
		preview += "\nAbout: " + ansi.Truncate(w.Description, max(8, workflowDisplayWidth(width)-7), "…")
	}
	for _, line := range fanOut {
		preview += "\n" + line
	}
	if steps := w.PreviewSteps(workflowDisplayWidth(width)); steps != "" {
		preview += "\nSteps:\n" + steps
	}
	inputs := w.InputNames()
	if len(inputs) > 0 {
		preview += "\nInputs: " + strings.Join(inputs, ", ")
	}
	preview += "\nRun: /workflow run " + name
	for _, key := range inputs {
		preview += " " + key + "=<value>"
	}
	return preview, nil
}

func (t *WorkflowTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.Execute(ctx, params, cwd)
}

// Execute launches the run as one background task and returns at once; the
// summary arrives as that task's completion notification.
func (t *WorkflowTool) Execute(_ context.Context, params map[string]any, _ string) toolresult.ToolResult {
	w, err := t.prepare(params)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	inputs := map[string]string{}
	if raw, ok := params["inputs"].(map[string]any); ok {
		for k, v := range raw {
			inputs[k] = fmt.Sprint(v)
		}
	}
	id := t.start(w, inputs, cmp.Or(tool.GetString(params, "description"), "Run workflow "+w.Name), 120)
	return toolresult.ToolResult{
		Success: true,
		Output: fmt.Sprintf("Workflow %s started in background.\nTask ID: %s\nStop: Ctrl+C (when foreground idle) or /workflow stop %s\n\n%s"+tool.BackgroundLaunchSuffix,
			w.Name, id, id, w.CompactProgressView(nil, 120)),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("[background] %s: %s", w.Name, id),
		},
	}
}

// Launch runs a saved workflow for a user's /workflow: the same checks and
// background task as a model's call, returning the plan's bounds and task id.
func (t *WorkflowTool) Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error) {
	w, err := t.prepare(map[string]any{"name": name})
	if err != nil {
		return "", "", err
	}
	var missing []string
	for _, key := range w.InputNames() {
		if _, ok := inputs[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("workflow %s needs input %s; use /workflow show %s to see the run command", name, strings.Join(missing, ", "), name)
	}
	turns, _ := w.Bounds()
	summary := fmt.Sprintf("Started workflow %s · up to %d turns · max %d parallel", w.Name, turns, w.MaxParallel)
	return summary, t.start(w, inputs, "/workflow "+name, width), nil
}

// start runs the workflow under one background task and returns its id.
func (t *WorkflowTool) start(w *workflow.Workflow, inputs map[string]string, description string, width int) string {
	width = workflowDisplayWidth(width)
	ctx, cancel := context.WithCancel(context.Background())
	bg := task.Default().CreateAgentTask(task.NewID(), "workflow", description, ctx, cancel)
	statuses := make(map[string]workflow.Status, len(w.Nodes))
	var events []workflow.ActivityEvent
	textBuffers := make(map[string]string)
	trimEvents := func() {
		if len(events) > workflow.ActivityWindow {
			events = events[len(events)-workflow.ActivityWindow:]
		}
	}
	addEvent := func(nodeID, worker, msg string) {
		events = append(events, workflow.ActivityEvent{Node: nodeID, Worker: worker, Text: msg})
		trimEvents()
	}
	addToolStart := func(nodeID, worker, id, call string) {
		events = append(events, workflow.ActivityEvent{Node: nodeID, Worker: worker, ToolID: id, Text: call})
		trimEvents()
	}
	finishTool := func(nodeID, worker, id, call, summary string, failed bool) {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Node == nodeID && events[i].Worker == worker && events[i].ToolID == id && id != "" && !events[i].ToolFinished {
				events[i].Text = call
				events[i].ToolResult = summary
				events[i].ToolFinished = true
				events[i].ToolFailed = failed
				return
			}
		}
		events = append(events, workflow.ActivityEvent{Node: nodeID, Worker: worker, ToolID: id, Text: call, ToolResult: summary, ToolFinished: true, ToolFailed: failed})
		trimEvents()
	}
	liveView := func(displayWidth int) string {
		visibleEvents := events
		if len(textBuffers) > 0 {
			visibleEvents = append([]workflow.ActivityEvent(nil), events...)
			keys := make([]string, 0, len(textBuffers))
			for key := range textBuffers {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				nodeID, worker, _ := strings.Cut(key, "·")
				if content := strings.Join(strings.Fields(textBuffers[key]), " "); content != "" {
					visibleEvents = append(visibleEvents, workflow.ActivityEvent{Node: nodeID, Worker: worker, Text: "› " + content})
				}
			}
		}
		return w.ActivityStreamView(visibleEvents, displayWidth) + "\n\n" +
			w.CompactProgressView(statuses, displayWidth)
	}
	var progressMu sync.Mutex
	bg.SetLiveViewRenderer(func(terminalWidth int) string {
		progressMu.Lock()
		defer progressMu.Unlock()
		return liveView(workflowDisplayWidth(terminalWidth))
	})
	bg.SetLiveView(liveView(width))

	go func() {
		defer cancel()
		runner := &nodeRunner{exec: t.executor, task: bg, name: w.Name}
		flushText := func(nodeID, worker string) {
			key := nodeID + "·" + worker
			if content := strings.Join(strings.Fields(textBuffers[key]), " "); content != "" {
				text := []rune(content)
				for len(text) > 0 {
					n := min(len(text), 160)
					addEvent(nodeID, worker, "› "+string(text[:n]))
					text = text[n:]
				}
			}
			delete(textBuffers, key)
		}
		runner.onTextDelta = func(nodeID, worker, fragment string) {
			progressMu.Lock()
			key := nodeID + "·" + worker
			textBuffers[key] += fragment
			if len([]rune(textBuffers[key])) >= 160 || strings.Contains(fragment, "\n") {
				flushText(nodeID, worker)
				bg.SetLiveView(liveView(width))
			}
			progressMu.Unlock()
		}
		runner.onTextEnd = func(nodeID, worker string) {
			progressMu.Lock()
			flushText(nodeID, worker)
			bg.SetLiveView(liveView(width))
			progressMu.Unlock()
		}
		runner.onActivity = func(nodeID, worker, msg string) {
			if strings.HasPrefix(msg, "Usage:") {
				return
			}
			msg = strings.Join(strings.Fields(msg), " ")
			if msg == "" {
				return
			}
			progressMu.Lock()
			addEvent(nodeID, worker, msg)
			bg.SetLiveView(liveView(width))
			progressMu.Unlock()
		}
		runner.onToolStart = func(nodeID, worker, id, call string) {
			progressMu.Lock()
			addToolStart(nodeID, worker, id, call)
			bg.SetLiveView(liveView(width))
			progressMu.Unlock()
		}
		runner.onToolResult = func(nodeID, worker, id, call, summary string, failed bool) {
			progressMu.Lock()
			finishTool(nodeID, worker, id, call, summary, failed)
			bg.SetLiveView(liveView(width))
			progressMu.Unlock()
		}
		runner.onWorkerStatus = func(nodeID, worker string, status workflow.Status) {
			progressMu.Lock()
			addEvent(nodeID, worker, string(status))
			bg.SetLiveView(liveView(width))
			progressMu.Unlock()
			bg.AppendProgress(nodeID + "·" + worker + ": " + string(status))
		}
		res := workflow.Run(ctx, w, runner, workflow.Options{
			Inputs: inputs,
			OnStatus: func(n *workflow.Node, s workflow.Status) {
				progressMu.Lock()
				statuses[n.ID] = s
				bg.SetLiveView(liveView(width))
				progressMu.Unlock()
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
	return bg.GetID()
}

func workflowDisplayWidth(terminalWidth int) int {
	if terminalWidth <= 0 {
		terminalWidth = 120
	}
	if terminalWidth < 30 {
		return max(8, terminalWidth-2)
	}
	return terminalWidth * 9 / 10
}

// nodeRunner is the workflow.NodeRunner seam: one node is one subagent turn
// through the same executor, and so the same permission gate, as Agent.
type nodeRunner struct {
	exec           tool.AgentExecutor
	task           *task.AgentTask
	name           string
	onActivity     func(nodeID, label, msg string)
	onTextDelta    func(nodeID, label, fragment string)
	onTextEnd      func(nodeID, label string)
	onToolStart    func(nodeID, worker, id, call string)
	onToolResult   func(nodeID, worker, id, call, summary string, failed bool)
	onWorkerStatus func(nodeID, worker string, status workflow.Status)
	steps          atomic.Int64
	tokens         atomic.Int64
}

func (r *nodeRunner) RunNode(ctx context.Context, c workflow.Call) (string, error) {
	n := c.Node
	var streamedText atomic.Bool
	label := n.ID
	if c.Label != "" {
		label += "·" + c.Label
		if r.onWorkerStatus != nil {
			r.onWorkerStatus(n.ID, c.Label, workflow.StatusRunning)
		}
	}
	res, err := r.exec.Run(ctx, tool.AgentExecRequest{
		Agent:            n.Config["agent"],
		Prompt:           c.Prompt,
		Description:      r.name + "/" + label,
		Background:       true,
		Model:            n.Config["model"],
		Mode:             n.Config["mode"],
		ActivityMaxChars: 320,
		OnActivity: func(msg string) {
			r.task.AppendProgress(label + " ▸ " + msg)
			if r.onActivity != nil {
				r.onActivity(n.ID, c.Label, msg)
			}
		},
		OnTextDelta: func(fragment string) {
			streamedText.Store(true)
			if r.onTextDelta != nil {
				r.onTextDelta(n.ID, c.Label, fragment)
			}
		},
		OnTextEnd: func() {
			if r.onTextEnd != nil {
				r.onTextEnd(n.ID, c.Label)
			}
		},
		OnToolStart: func(id, call string) {
			r.task.AppendProgress(label + " ▸ " + call)
			if r.onToolStart != nil {
				r.onToolStart(n.ID, c.Label, id, call)
			}
		},
		OnToolResult: func(id, call, output string, toolErr error) {
			summary := workflowToolResultSummary(output, toolErr)
			marker := "✓"
			if toolErr != nil {
				marker = "✗"
			}
			r.task.AppendProgress(label + " ▸ " + marker + " " + call + " · " + summary)
			if r.onToolResult != nil {
				r.onToolResult(n.ID, c.Label, id, call, summary, toolErr != nil)
			}
		},
	})
	if err != nil {
		if r.onActivity != nil {
			r.onActivity(n.ID, c.Label, "Error: "+err.Error())
		}
		r.workerFinished(n.ID, c.Label, workflow.StatusFailed)
		return "", err
	}
	steps := r.steps.Add(int64(res.StepCount))
	tokens := r.tokens.Add(int64(res.TotalInputTokens + res.TotalOutputTokens))
	r.task.UpdateProgress(int(steps), int(tokens))
	if !res.Success {
		r.workerFinished(n.ID, c.Label, workflow.StatusFailed)
		return res.Content, fmt.Errorf("%s", res.Error)
	}
	if preview := workflowResultPreview(res.Content); preview != "" && !streamedText.Load() {
		activity := "Result: " + preview
		r.task.AppendProgress(label + " ▸ " + activity)
		if r.onActivity != nil {
			r.onActivity(n.ID, c.Label, activity)
		}
	}
	r.workerFinished(n.ID, c.Label, workflow.StatusSucceeded)
	return res.Content, nil
}

func workflowToolResultSummary(output string, toolErr error) string {
	if toolErr != nil {
		return ansi.Truncate(strings.Join(strings.Fields(ansi.Strip(toolErr.Error())), " "), 140, "…")
	}
	if strings.TrimSpace(output) == "" || output == "(no output)" {
		return "no output"
	}
	lines := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		lines++
	}
	head := output[:min(len(output), 4096)]
	first := ""
	for line := range strings.SplitSeq(head, "\n") {
		first = strings.Join(strings.Fields(ansi.Strip(line)), " ")
		if first != "" {
			break
		}
	}
	first = ansi.Truncate(first, 56, "…")
	if lines == 1 && len(output) <= 160 {
		return first
	}
	size := fmt.Sprintf("%d B", len(output))
	if len(output) >= 1024 {
		size = fmt.Sprintf("%.1f KiB", float64(len(output))/1024)
	}
	summary := fmt.Sprintf("%d lines · %s", lines, size)
	if first != "" {
		summary += " · " + first
	}
	return summary
}

func workflowResultPreview(content string) string {
	text := []rune(strings.Join(strings.Fields(content), " "))
	if len(text) > 240 {
		return string(text[:239]) + "…"
	}
	return string(text)
}

func (r *nodeRunner) workerFinished(nodeID, worker string, status workflow.Status) {
	if worker != "" && r.onWorkerStatus != nil {
		r.onWorkerStatus(nodeID, worker, status)
	}
}

func init() {
	tool.Register(NewWorkflowTool())
}
