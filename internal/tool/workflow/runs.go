package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/genai-io/san/internal/task"
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
		if err != nil {
			release()
			return "", "", err
		}
	} else {
		states, err = workflow.Retry(w, r.Nodes)
		if err != nil {
			release()
			return "", "", err
		}
	}
	r.Nodes = states
	taskID, err := t.launchRecord(w, *r, "Resume workflow "+w.Name, release, 120, "")
	return describe(w, ""), taskID, err
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
