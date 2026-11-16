package flow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/task"
)

func (s *Service) runStore() *Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		s.store = NewStore("")
	}
	return s.store
}

// Runs lists run records, newest first, with orphaned runs reconciled.
func (s *Service) Runs() ([]RunRecord, error) {
	records, err := s.runStore().List()
	if err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].Status != RunRunning {
			continue
		}
		r, err := s.reconcile(&records[i])
		if err != nil {
			return nil, err
		}
		records[i] = *r
	}
	return records, nil
}

// GetRun returns a run by run ID or by the task ID shown at launch, with an
// orphaned run reconciled first.
func (s *Service) GetRun(id string) (*RunRecord, error) {
	r, err := s.lookup(id)
	if err != nil {
		return nil, err
	}
	return s.reconcile(r)
}

// reconcile marks a record still running with no lock holder as interrupted:
// its executor is gone, in this process or another.
func (s *Service) reconcile(r *RunRecord) (*RunRecord, error) {
	if r.Status != RunRunning {
		return r, nil
	}
	st := s.runStore()
	release, err := st.Acquire(r.ID)
	if errors.Is(err, ErrRunActive) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	defer release()
	err = st.Update(r.ID, func(r *RunRecord) {
		if r.Status == RunRunning {
			r.Status = RunInterrupted
			r.Error = "executor interrupted"
		}
	})
	if err != nil {
		return nil, err
	}
	return st.Get(r.ID)
}

// lookup resolves a run ID, falling back to the task ID of its latest execution.
func (s *Service) lookup(id string) (*RunRecord, error) {
	st := s.runStore()
	r, err := st.Get(id)
	if err == nil {
		return r, nil
	}
	// A readable-but-broken record is an error; only a missing one falls back.
	if checkID(id) == nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if r, err := st.FindByTask(id); err == nil {
		return r, nil
	}
	return nil, fmt.Errorf("no flow run %s: %w", id, os.ErrNotExist)
}

// StopRun stops a live run named by run ID or task ID.
func (s *Service) StopRun(id string) error {
	s.mu.Lock()
	bg := s.live[id]
	if bg == nil {
		for _, live := range s.live {
			if live.GetID() == id {
				bg = live
			}
		}
	}
	s.mu.Unlock()
	if bg == nil {
		return fmt.Errorf("run %s has no live executor in this process", id)
	}
	return bg.Stop()
}

// ResumeRun reruns a stored snapshot — failed roots and their descendants, or
// from and its descendants when from is set — and returns the new task ID.
func (s *Service) ResumeRun(id, from string, width int) (string, error) {
	st := s.runStore()
	found, err := s.lookup(id)
	if err != nil {
		return "", err
	}
	release, err := st.Acquire(found.ID)
	if err != nil {
		return "", err
	}
	r, err := st.Get(found.ID)
	var b *Blueprint
	if err == nil {
		b, err = Parse(r.Definition)
	}
	if err == nil {
		err = s.preflight(b)
	}
	if err == nil {
		err = validateInputs(b, r.Inputs)
	}
	if err == nil && b.hasMain() {
		if s.tasks == nil || r.SessionID == "" || r.SessionID != s.tasks.SessionID() {
			err = fmt.Errorf("main flow resume requires its original session")
		}
		if err == nil && from == "" {
			for _, n := range b.Nodes {
				c := r.Nodes[n.ID]
				if n.Executor == "main" && (c.Status == StatusFailed || c.Status == StatusRunning) {
					err = fmt.Errorf("main node %s may already have side effects; confirm rerun with explicit from", n.ID)
					break
				}
			}
		}
	}
	if err == nil {
		b.Name = r.Name
		if from != "" {
			r.Nodes, err = Invalidate(b, r.Nodes, from)
		} else {
			r.Nodes, err = Retry(b, r.Nodes)
		}
	}
	if err != nil {
		release()
		return "", err
	}
	return s.launchRecord(b, *r, "Resume flow "+b.Name, release, width)
}

// start persists a snapshot before launching its background task.
func (s *Service) start(b *Blueprint, inputs map[string]string, description string, width int) (string, error) {
	if err := s.runStore().Prune(maxFinishedRuns); err != nil {
		log.Logger().Warn("flow: pruning run history failed", zap.Error(err))
	}
	return s.startRecord(b, RunRecord{ID: task.NewID(), Name: b.Name, Definition: b.Source, Inputs: maps.Clone(inputs)}, description, width)
}

func (s *Service) startRecord(b *Blueprint, r RunRecord, description string, width int) (string, error) {
	release, err := s.runStore().Acquire(r.ID)
	if err != nil {
		return "", err
	}
	return s.launchRecord(b, r, description, release, width)
}

func (s *Service) launchRecord(b *Blueprint, r RunRecord, description string, release func(), width int) (string, error) {
	st := s.runStore()
	ctx, cancel := context.WithCancel(context.Background())
	id := task.NewID()
	s.mu.Lock()
	executor, mainExecutor := s.executor, s.mainExecutor
	s.mu.Unlock()
	var main MainRunExecutor
	if b.hasMain() {
		fail := func(err error) (string, error) { cancel(); release(); return "", err }
		if s.tasks == nil || s.tasks.SessionID() == "" {
			return fail(fmt.Errorf("main flow requires an active session"))
		}
		if r.SessionID != "" && r.SessionID != s.tasks.SessionID() {
			return fail(fmt.Errorf("main flow belongs to a different session"))
		}
		r.SessionID = s.tasks.SessionID()
		ctx = task.WithSessionID(ctx, r.SessionID)
		if mainExecutor == nil {
			return fail(fmt.Errorf("main executor not configured"))
		}
		var err error
		main, err = mainExecutor.Open(ctx, MainRunInfo{ID: r.ID, TaskID: id, SessionID: r.SessionID, Instructions: r.Instructions})
		if err != nil {
			if main != nil {
				main.Close(err)
			}
			return fail(err)
		}
		if main == nil {
			return fail(fmt.Errorf("main executor returned no lease"))
		}
	}
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
	if err := st.Put(r); err != nil {
		if main != nil {
			main.Close(err)
		}
		cancel()
		release()
		return "", err
	}
	bg := s.tasks.CreateSchedulerTask(id, b.Title(), description, ctx, cancel)
	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]*task.AgentTask{}
	}
	s.live[r.ID] = bg
	s.mu.Unlock()
	go func() {
		var runErr error
		if main != nil {
			defer func() { main.Close(runErr) }()
		}
		defer cancel()
		defer release()
		defer func() { s.mu.Lock(); delete(s.live, r.ID); s.mu.Unlock() }()
		var persistMu sync.Mutex
		var persistErr error
		update := func(fn func(*RunRecord)) error {
			persistMu.Lock()
			defer persistMu.Unlock()
			if persistErr != nil {
				return persistErr
			}
			persistErr = st.Update(r.ID, fn)
			if persistErr != nil {
				cancel()
			}
			return persistErr
		}
		snapshots := map[string]string{}
		for _, n := range b.Nodes {
			if c := r.Nodes[n.ID]; n.Executor == "main" && c.Status == StatusSucceeded {
				snapshots[n.ID] = c.Instructions
			}
		}
		runner := &nodeRunner{exec: executor, main: main, task: bg, name: b.Name, blueprint: b, instructions: snapshots, onInstructions: func(n *Node, instructions string) error {
			return update(func(r *RunRecord) {
				r.Instructions = instructions
				c := r.Nodes[n.ID]
				c.Instructions = instructions
				r.Nodes[n.ID] = c
			})
		}}
		onStatus := attachLiveView(b, bg, width)
		res := Run(ctx, b, runner, Options{Route: mainRoute(ctx, b, main), Inputs: r.Inputs, Resume: r.Nodes, OnStatus: onStatus, OnCheckpoint: func(n *Node, c Checkpoint) error {
			if n.Executor == "main" {
				c.Instructions = runner.snapshot(n.ID)
			}
			return update(func(r *RunRecord) { r.Nodes[n.ID] = c })
		}})
		output := res.Summary(b)
		status := RunSucceeded
		if ctx.Err() != nil {
			status = RunStopped
			runErr = ctx.Err()
		} else if res.Failed(b) {
			status = RunFailed
			runErr = fmt.Errorf("flow %s failed: %v", b.Name, res.Failures(b))
		}
		persistMu.Lock()
		if persistErr != nil {
			runErr = persistErr
			status = RunFailed
		}
		persistMu.Unlock()
		err := st.Update(r.ID, func(r *RunRecord) {
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
		if runErr == nil {
			bg.SetResult(res.FinalOutput(b))
			if soleMainSink(b, res) {
				bg.MarkResultDisplayed()
			}
		} else {
			bg.AppendOutput([]byte(res.FinalOutput(b)))
		}
		bg.Complete(runErr)
	}()
	return id, nil
}

// attachLiveView publishes scheduler progress in the existing task area.
func attachLiveView(b *Blueprint, bg *task.AgentTask, width int) func(*Node, Status) {
	width = fitWidth(width)
	statuses := make(map[string]Status, len(b.Nodes))
	liveView := func(displayWidth int) string {
		return b.CompactProgressView(statuses, displayWidth)
	}
	var progressMu sync.Mutex
	bg.SetLiveViewRenderer(func(terminalWidth int) string {
		progressMu.Lock()
		defer progressMu.Unlock()
		return liveView(fitWidth(terminalWidth))
	})
	bg.SetLiveView(liveView(width))

	return func(n *Node, st Status) {
		progressMu.Lock()
		statuses[n.ID] = st
		bg.SetLiveView(liveView(width))
		progressMu.Unlock()
		bg.AppendProgress(n.ID + ": " + string(st))
	}
}

func fitWidth(terminalWidth int) int {
	if terminalWidth <= 0 {
		terminalWidth = 120
	}
	if terminalWidth < 30 {
		return max(8, terminalWidth-2)
	}
	return terminalWidth * 9 / 10
}
