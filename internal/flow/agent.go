package flow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func (t *Service) runStore() *Store {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.store == nil {
		t.store = NewStore("")
	}
	return t.store
}

func (t *Service) Runs() ([]RunRecord, error) {
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

// InspectRun returns a run by run ID or by the task ID shown at launch. A
// record still marked running with no lock holder lost its executor and is
// reported as interrupted.
func (t *Service) InspectRun(id string) (*RunRecord, error) {
	s := t.runStore()
	r, err := t.lookup(id)
	if err != nil {
		return nil, err
	}
	if r.Status != RunRunning {
		return r, nil
	}
	release, err := s.Acquire(r.ID)
	if err != nil {
		if errors.Is(err, ErrRunActive) {
			return r, nil
		}
		return nil, err
	}
	defer release()
	err = s.Update(r.ID, func(r *RunRecord) {
		if r.Status == RunRunning {
			r.Status = RunInterrupted
			r.Error = "executor interrupted"
		}
	})
	if err != nil {
		return nil, err
	}
	return s.Get(r.ID)
}

// lookup resolves a run ID, falling back to the task ID of its latest execution.
func (t *Service) lookup(id string) (*RunRecord, error) {
	s := t.runStore()
	r, err := s.Get(id)
	if err == nil || !errors.Is(err, os.ErrNotExist) && checkID(id) == nil {
		return r, err
	}
	if r, err := s.FindByTask(id); err == nil {
		return r, nil
	}
	return nil, fmt.Errorf("no flow run %s: %w", id, os.ErrNotExist)
}

// StopRun stops a live run named by run ID or task ID.
func (t *Service) StopRun(id string) error {
	t.mu.Lock()
	bg := t.live[id]
	if bg == nil {
		for _, live := range t.live {
			if live.GetID() == id {
				bg = live
			}
		}
	}
	t.mu.Unlock()
	if bg == nil {
		return fmt.Errorf("run %s has no live executor in this process", id)
	}
	return bg.Stop()
}

// ResumeRun reruns a stored snapshot: failed roots and their descendants, or
// from and its descendants when from is set.
func (t *Service) ResumeRun(id, from string, width int) (string, string, error) {
	s := t.runStore()
	found, err := t.lookup(id)
	if err != nil {
		return "", "", err
	}
	release, err := s.Acquire(found.ID)
	if err != nil {
		return "", "", err
	}
	r, err := s.Get(found.ID)
	if err != nil {
		release()
		return "", "", err
	}
	w, err := Parse(r.Definition)
	if err == nil {
		err = t.preflight(w)
	}
	if err == nil {
		err = validateInputs(w, r.Inputs)
	}
	if err != nil {
		release()
		return "", "", err
	}
	w.Name = r.Name
	states := r.Nodes
	if from != "" {
		states, err = Invalidate(w, states, from)
	} else {
		states, err = Retry(w, states)
	}
	if err != nil {
		release()
		return "", "", err
	}
	r.Nodes = states
	taskID, err := t.launchRecord(w, *r, "Resume flow "+w.Name, release, width)
	return describe(w, "") + "\nRun ID: " + r.ID, taskID, err
}

// start persists a snapshot before launching its background task.
func (t *Service) start(w *Blueprint, inputs map[string]string, description string, width int) (runID, taskID string, err error) {
	s := t.runStore()
	if err := s.Prune(maxFinishedRuns); err != nil {
		log.Logger().Warn("flow: pruning run history failed", zap.Error(err))
	}
	runID = task.NewID()
	taskID, err = t.startRecord(w, RunRecord{ID: runID, Name: w.Name, Definition: w.Source, Inputs: maps.Clone(inputs)}, description, width)
	return runID, taskID, err
}

func (t *Service) startRecord(w *Blueprint, r RunRecord, description string, width int) (string, error) {
	release, err := t.runStore().Acquire(r.ID)
	if err != nil {
		return "", err
	}
	return t.launchRecord(w, r, description, release, width)
}

func (t *Service) launchRecord(w *Blueprint, r RunRecord, description string, release func(), width int) (string, error) {
	s := t.runStore()
	ctx, cancel := context.WithCancel(context.Background())
	id := task.NewID()
	now := time.Now()
	r.TaskID = id
	r.Status = RunRunning
	r.UpdatedAt = now
	r.FinishedAt = time.Time{}
	r.Error = ""
	if r.StartedAt.IsZero() {
		r.StartedAt = now
	}
	if r.Nodes == nil {
		r.Nodes = map[string]Checkpoint{}
	}
	if err := s.Put(r); err != nil {
		cancel()
		release()
		return "", err
	}
	executor := t.executor
	bg := t.tasks.CreateSchedulerTask(id, w.Title(), description, ctx, cancel)
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
		update := func(fn func(*RunRecord)) error {
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
		runner := &nodeRunner{exec: executor, task: bg, name: w.Name}
		onStatus := attachLiveView(w, bg, width)
		res := Run(ctx, w, runner, Options{Inputs: r.Inputs, Resume: r.Nodes, OnStatus: onStatus, OnCheckpoint: func(n *Node, c Checkpoint) error {
			return update(func(r *RunRecord) { r.Nodes[n.ID] = c })
		}})
		output := res.Summary(w)
		bg.AppendOutput([]byte(output))
		var runErr error
		status := RunSucceeded
		if ctx.Err() != nil {
			status = RunStopped
			runErr = ctx.Err()
		} else if res.Failed(w) {
			status = RunFailed
			runErr = fmt.Errorf("flow %s failed: %v", w.Name, res.Failures(w))
		}
		persistMu.Lock()
		if persistErr != nil {
			runErr = persistErr
			status = RunFailed
		}
		persistMu.Unlock()
		err := s.Update(r.ID, func(r *RunRecord) {
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

// attachLiveView publishes scheduler progress in the existing task area.
func attachLiveView(w *Blueprint, bg *task.AgentTask, width int) func(*Node, Status) {
	width = flowDisplayWidth(width)
	statuses := make(map[string]Status, len(w.Nodes))
	liveView := func(displayWidth int) string {
		return w.CompactProgressView(statuses, displayWidth)
	}
	var progressMu sync.Mutex
	bg.SetLiveViewRenderer(func(terminalWidth int) string {
		progressMu.Lock()
		defer progressMu.Unlock()
		return liveView(flowDisplayWidth(terminalWidth))
	})
	bg.SetLiveView(liveView(width))

	return func(n *Node, s Status) {
		progressMu.Lock()
		statuses[n.ID] = s
		bg.SetLiveView(liveView(width))
		progressMu.Unlock()
		bg.AppendProgress(n.ID + ": " + string(s))
	}
}

func flowDisplayWidth(terminalWidth int) int {
	if terminalWidth <= 0 {
		terminalWidth = 120
	}
	if terminalWidth < 30 {
		return max(8, terminalWidth-2)
	}
	return terminalWidth * 9 / 10
}

// nodeRunner is the NodeRunner seam: one node is one subagent turn
// through the same executor, and so the same permission gate, as Agent.
// Progress goes to the scheduler task's live tail only; the native child task
// owns the complete transcript.
type nodeRunner struct {
	exec    tool.AgentExecutor
	task    *task.AgentTask
	name    string
	usageMu sync.Mutex
	steps   int
	tokens  int
}

func (r *nodeRunner) RunNode(ctx context.Context, c Call) (string, error) {
	n := c.Node
	label := n.ID
	if c.Label != "" {
		label += "·" + c.Label
	}
	res, err := r.exec.Run(ctx, tool.AgentExecRequest{
		OnIdentity: func(identity toolresult.AgentIdentity) {
			if c.OnExecution != nil {
				c.OnExecution(Execution{TaskID: identity.ID, SessionID: identity.SessionID})
			}
		},
		Flow:             &tool.FlowOrigin{TaskID: r.task.GetID(), Name: r.name, NodeID: n.ID, WorkerLabel: c.Label},
		Agent:            n.Config["agent"],
		Prompt:           c.Prompt,
		Description:      label,
		Background:       true,
		Model:            n.Config["model"],
		Mode:             n.Config["mode"],
		ActivityMaxChars: 320,
		OnActivity: func(msg string) {
			r.task.AppendProgress(label + " ▸ " + msg)
		},
		OnToolStart: func(_, call string) {
			r.task.AppendProgress(label + " ▸ " + call)
		},
		OnToolResult: func(_, call, output string, toolErr error) {
			marker := "✓"
			if toolErr != nil {
				marker = "✗"
			}
			r.task.AppendProgress(label + " ▸ " + marker + " " + call + " · " + flowToolResultSummary(output, toolErr))
		},
	})
	output := ""
	if res != nil {
		if c.OnExecution != nil && (res.Identity.ID != "" || res.Identity.SessionID != "") {
			c.OnExecution(Execution{TaskID: res.Identity.ID, SessionID: res.Identity.SessionID})
		}
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
		return output, err
	}
	if preview := flowResultPreview(res.Content); preview != "" {
		r.task.AppendProgress(label + " ▸ Result: " + preview)
	}
	return res.Content, nil
}
func flowToolResultSummary(output string, toolErr error) string {
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

func flowResultPreview(content string) string {
	text := []rune(strings.Join(strings.Fields(content), " "))
	if len(text) > 240 {
		return string(text[:239]) + "…"
	}
	return string(text)
}
