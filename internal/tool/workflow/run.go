package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/workflow"
)

func (t *WorkflowTool) runStore() *workflow.Store {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.store == nil {
		t.store = workflow.NewStore("")
	}
	return t.store
}

func (t *WorkflowTool) Runs() ([]workflow.RunRecord, error) {
	records, err := t.runStore().List()
	if err != nil {
		return nil, err
	}
	for i := range records {
		r, err := t.InspectRun(records[i].ID)
		if err != nil {
			return nil, err
		}
		records[i] = *r
	}
	return records, nil
}

func (t *WorkflowTool) InspectRun(id string) (*workflow.RunRecord, error) {
	s := t.runStore()
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if r.Status != workflow.RunRunning {
		return r, nil
	}
	release, err := s.Acquire(id)
	if err != nil {
		if errors.Is(err, workflow.ErrRunActive) {
			return r, nil
		}
		return nil, err
	}
	defer release()
	err = s.Update(id, func(r *workflow.RunRecord) {
		if r.Status == workflow.RunRunning {
			r.Status = workflow.RunInterrupted
			r.Error = "executor interrupted"
		}
	})
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

func (t *WorkflowTool) StopRun(id string) error {
	t.mu.Lock()
	bg := t.live[id]
	t.mu.Unlock()
	if bg == nil {
		return fmt.Errorf("run %s has no live executor in this process", id)
	}
	return bg.Stop()
}

func (t *WorkflowTool) ResumeRun(id, from string) (string, string, error) {
	s := t.runStore()
	release, err := s.Acquire(id)
	if err != nil {
		return "", "", err
	}
	r, err := s.Get(id)
	if err != nil {
		release()
		return "", "", err
	}
	w, err := t.prepare(map[string]any{"definition": r.Definition})
	if err != nil {
		release()
		return "", "", err
	}
	w.Name = r.Name
	states := r.Nodes
	if from != "" {
		states, err = workflow.Invalidate(w, states, from)
	} else {
		states, err = workflow.Retry(w, states)
	}
	if err != nil {
		release()
		return "", "", err
	}
	r.Nodes = states
	taskID, err := t.launchRecord(w, *r, "Resume workflow "+w.Name, release, 120, "")
	return describe(w, ""), taskID, err
}

// start persists a snapshot before launching its background task.
func (t *WorkflowTool) start(w *workflow.Workflow, inputs map[string]string, description string, width int, parentToolCallID string) (string, error) {
	return t.startRecord(w, workflow.RunRecord{ID: task.NewID(), Name: w.Name, Definition: w.Source, Inputs: maps.Clone(inputs)}, description, width, parentToolCallID)
}

func (t *WorkflowTool) startRecord(w *workflow.Workflow, r workflow.RunRecord, description string, width int, parentToolCallID string) (string, error) {
	release, err := t.runStore().Acquire(r.ID)
	if err != nil {
		return "", err
	}
	return t.launchRecord(w, r, description, release, width, parentToolCallID)
}

func (t *WorkflowTool) launchRecord(w *workflow.Workflow, r workflow.RunRecord, description string, release func(), width int, parentToolCallID string) (string, error) {
	s := t.runStore()
	ctx, cancel := context.WithCancel(context.Background())
	id := task.NewID()
	now := time.Now()
	r.TaskID = id
	r.Status = workflow.RunRunning
	r.UpdatedAt = now
	r.FinishedAt = time.Time{}
	r.Error = ""
	if r.StartedAt.IsZero() {
		r.StartedAt = now
	}
	if r.Nodes == nil {
		r.Nodes = map[string]workflow.Checkpoint{}
	}
	if r.Activity == nil {
		r.Activity = map[string][]string{}
	}
	if err := s.Put(r); err != nil {
		cancel()
		release()
		return "", err
	}
	executor := t.executor
	bg := task.Default().CreateAgentTask(id, "workflow", description, ctx, cancel)
	t.mu.Lock()
	if t.live == nil {
		t.live = map[string]*task.AgentTask{}
	}
	t.live[r.ID] = bg
	t.mu.Unlock()
	go func() {
		defer cancel()
		defer release()
		defer func() { t.mu.Lock(); delete(t.live, r.ID); t.mu.Unlock() }()
		var persistMu sync.Mutex
		var persistErr error
		update := func(fn func(*workflow.RunRecord)) error {
			persistMu.Lock()
			defer persistMu.Unlock()
			if persistErr != nil {
				return persistErr
			}
			persistErr = s.Update(r.ID, fn)
			if persistErr != nil {
				cancel()
			}
			return persistErr
		}
		runner := &nodeRunner{exec: executor, task: bg, name: w.Name, parentToolCallID: parentToolCallID}
		onStatus := attachLiveView(w, bg, runner, width)
		recordActivity := func(node, worker, msg string) {
			if worker != "" {
				msg = worker + " ▸ " + msg
			}
			_ = update(func(r *workflow.RunRecord) {
				if r.Activity == nil {
					r.Activity = map[string][]string{}
				}
				a := append(r.Activity[node], msg)
				if len(a) > 100 {
					a = a[len(a)-100:]
				}
				r.Activity[node] = a
			})
		}
		liveActivity := runner.onActivity
		runner.onActivity = func(node, worker, msg string) {
			liveActivity(node, worker, msg)
			recordActivity(node, worker, msg)
		}
		liveToolStart, liveToolResult := runner.onToolStart, runner.onToolResult
		runner.onToolStart = func(node, worker, id, call string) {
			liveToolStart(node, worker, id, call)
			recordActivity(node, worker, call)
		}
		runner.onToolResult = func(node, worker, id, call, summary string, failed bool) {
			liveToolResult(node, worker, id, call, summary, failed)
			marker := "✓ "
			if failed {
				marker = "✗ "
			}
			recordActivity(node, worker, marker+call+" · "+summary)
		}
		res := workflow.Run(ctx, w, runner, workflow.Options{Inputs: r.Inputs, Resume: r.Nodes, OnStatus: onStatus, OnCheckpoint: func(n *workflow.Node, c workflow.Checkpoint) error {
			return update(func(r *workflow.RunRecord) { r.Nodes[n.ID] = c })
		}})
		output := res.Summary(w)
		bg.AppendOutput([]byte(output))
		var runErr error
		status := workflow.RunSucceeded
		if ctx.Err() != nil {
			status = workflow.RunStopped
			runErr = ctx.Err()
		} else if res.Failed(w) {
			status = workflow.RunFailed
			runErr = fmt.Errorf("workflow %s failed: %v", w.Name, res.Failures(w))
		}
		persistMu.Lock()
		if persistErr != nil {
			runErr = persistErr
			status = workflow.RunFailed
		}
		persistMu.Unlock()
		err := s.Update(r.ID, func(r *workflow.RunRecord) {
			r.Status = status
			r.Output = output
			r.FinishedAt = time.Now()
			if runErr != nil {
				r.Error = runErr.Error()
			}
		})
		if err != nil {
			runErr = err
		}
		bg.Complete(runErr)
	}()
	return id, nil
}

// attachLiveView connects the executor's events to the shared task-area view.
func attachLiveView(w *workflow.Workflow, bg *task.AgentTask, runner *nodeRunner, width int) func(*workflow.Node, workflow.Status) {
	width = workflowDisplayWidth(width)
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

	{
		flushText := func(nodeID, worker string) {
			key := nodeID + "·" + worker
			if content := strings.Join(strings.Fields(textBuffers[key]), " "); content != "" {
				text := []rune(content)
				for len(text) > 0 {
					n := min(len(text), 160)
					events = append(events, workflow.ActivityEvent{Node: nodeID, Worker: worker, Text: "› " + string(text[:n]), TextPending: true})
					trimEvents()
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
		runner.onTextEnd = func(nodeID, worker string, err error) {
			progressMu.Lock()
			if err != nil {
				delete(textBuffers, nodeID+"·"+worker)
				events = slices.DeleteFunc(events, func(event workflow.ActivityEvent) bool {
					return event.Node == nodeID && event.Worker == worker && event.TextPending
				})
			} else {
				flushText(nodeID, worker)
				for i := range events {
					if events[i].Node == nodeID && events[i].Worker == worker {
						events[i].TextPending = false
					}
				}
			}
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
	}
	return func(n *workflow.Node, s workflow.Status) {
		progressMu.Lock()
		statuses[n.ID] = s
		bg.SetLiveView(liveView(width))
		progressMu.Unlock()
		bg.AppendProgress(n.ID + ": " + string(s))
	}
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
	exec             tool.AgentExecutor
	task             *task.AgentTask
	name             string
	parentToolCallID string
	onActivity       func(nodeID, label, msg string)
	onTextDelta      func(nodeID, label, fragment string)
	onTextEnd        func(nodeID, label string, err error)
	onToolStart      func(nodeID, worker, id, call string)
	onToolResult     func(nodeID, worker, id, call, summary string, failed bool)
	onWorkerStatus   func(nodeID, worker string, status workflow.Status)
	usageMu          sync.Mutex
	steps            int
	tokens           int
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
		ParentToolCallID: r.parentToolCallID,
		Workflow:         &tool.WorkflowOrigin{TaskID: r.task.GetID(), Name: r.name, NodeID: n.ID, WorkerLabel: c.Label},
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
		OnTextEnd: func(err error) {
			if r.onTextEnd != nil {
				r.onTextEnd(n.ID, c.Label, err)
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
	output := ""
	if res != nil {
		output = res.Content
		r.usageMu.Lock()
		r.steps += res.StepCount
		r.tokens += res.TotalInputTokens + res.TotalOutputTokens
		r.task.UpdateProgress(r.steps, r.tokens)
		r.usageMu.Unlock()
		if err == nil && !res.Success {
			err = fmt.Errorf("%s", res.Error)
		}
	} else if err == nil {
		err = fmt.Errorf("agent returned no result")
	}
	if err != nil {
		if res != nil {
			partial := output
			if res.AgentID != "" {
				partial += "\n\nAgentID: " + res.AgentID
			}
			if res.OutputFile != "" {
				partial += "\n\nTranscript: " + res.OutputFile
			}
			if strings.TrimSpace(partial) != "" {
				r.task.AppendOutput([]byte(fmt.Sprintf("\n## %s (unfinished)\n%s\n", label, partial)))
			}
		}
		if r.onActivity != nil {
			r.onActivity(n.ID, c.Label, "Error: "+err.Error())
		}
		r.workerFinished(n.ID, c.Label, workflow.StatusFailed)
		return output, err
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
