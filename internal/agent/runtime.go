package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/task"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

var (
	ErrRuntimeInactive = errors.New("agent session is not active")
	ErrRuntimeStopped  = errors.New("agent session stopped before the message was accepted")
)

type run struct {
	agent   core.Agent
	cancel  context.CancelFunc
	done    chan struct{}
	pending []core.Message // accepted inputs not yet announced by MessageAdded; Runtime.mu guards
	stopped bool
	onEvent func(core.Event)
}

type Runtime struct {
	mu         sync.RWMutex
	run        *run
	outbox     <-chan core.Event // retained until reset/replacement so final events can drain
	messages   []core.Message    // native chain retained between runs
	permGate   *PermissionGate
	pluginRoot string // see SetPluginRoot
	// build is set by tests to substitute the agent; nil means buildAgent.
	build func(BuildParams) (core.Agent, *PermissionGate, error)
}

func (s *Runtime) Start(params BuildParams, messages []core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.run != nil {
		return fmt.Errorf("agent session already active")
	}
	run := &run{done: make(chan struct{}), onEvent: params.OnEvent}
	params.OnEvent = func(ev core.Event) {
		switch e := ev.(type) {
		case sdkagent.MessageAdded:
			s.mu.Lock()
			run.pending = slices.DeleteFunc(run.pending, func(m core.Message) bool { return m.ID == e.Message.ID })
			s.mu.Unlock()
		case core.AgentStopped:
			s.mu.Lock()
			run.stopped = true
			s.mu.Unlock()
		}
		if run.onEvent != nil {
			run.onEvent(ev)
		}
	}

	builder := s.build
	if builder == nil {
		builder = buildAgent
	}
	ag, pg, err := builder(params)
	if err != nil {
		return err
	}

	if len(messages) > 0 {
		ag.SetMessages(messages)
	}

	ctx, cancel := context.WithCancel(task.WithSessionID(context.Background(), params.SessionID))
	run.agent, run.cancel = ag, cancel
	s.run = run
	s.outbox = ag.Outbox()
	s.messages = nil
	s.permGate = pg
	go s.execute(run, ctx)

	return nil
}

// execute owns the run generation: the session stays active until Run returns,
// so a stop and a restart can never overlap. Run's error already reaches the
// app as a core.AgentStopped event.
func (s *Runtime) execute(run *run, ctx context.Context) {
	_ = run.agent.Run(ctx)

	s.mu.Lock()
	run.stopped = true
	messages := run.agent.Messages()
	pending := slices.Clone(run.pending)
	s.mu.Unlock()
	// Input can arrive after the SDK's final boundary but before the mailbox stops.
	for _, msg := range pending {
		messages = append(messages, msg)
		if run.onEvent != nil {
			run.onEvent(sdkagent.MessageAdded{Message: msg})
		}
	}
	s.mu.Lock()
	s.messages = messages
	s.run = nil
	s.permGate = nil
	s.pluginRoot = ""
	close(run.done)
	s.mu.Unlock()
}

const runtimeStopTimeout = 2 * time.Second

// Stop performs a bounded graceful shutdown; a run that outlives the deadline
// is logged and stays active. Callers that need a different deadline use
// StopContext.
func (s *Runtime) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeStopTimeout)
	defer cancel()
	if err := s.StopContext(ctx); err != nil {
		log.Logger().Warn("agent session did not stop before deadline", zap.Error(err))
	}
}

// StopContext cancels the active run and waits for that exact generation to
// finish. The run remains active until Run returns, so a concurrent Start can
// never overlap the previous agent's teardown.
func (s *Runtime) StopContext(ctx context.Context) error {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	if run == nil {
		return nil
	}

	run.cancel()
	select {
	case run.agent.Inbox() <- core.Inbound{Signal: core.SigStop}:
	default:
	}

	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Runtime) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.run != nil
}

// Messages returns the native conversation, including after a run exits.
// Provider errors and deliberate stops retain the same chain for retry.
func (s *Runtime) Messages() []core.Message {
	s.mu.RLock()
	run := s.run
	messages := slices.Clone(s.messages)
	s.mu.RUnlock()
	if run == nil {
		return messages
	}
	return run.agent.Messages()
}

// SetMessages replaces the native chain, including before the first run.
// Resume and inactive compaction use this without rebuilding from UI rows.
func (s *Runtime) SetMessages(messages []core.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil {
		s.run.agent.SetMessages(messages)
		return
	}
	s.messages = slices.Clone(messages)
	s.outbox = nil
}

// Reset stops the run and discards its conversation once shutdown completes.
func (s *Runtime) Reset() {
	s.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		s.messages = nil
		s.outbox = nil
	}
}

// Send delivers a message to the active agent. Callers create the Message once
// at the input boundary so the TUI projection, the inbox, and the transcript
// share one ID; a message without one is stamped on acceptance.
func (s *Runtime) Send(msg core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.run
	if run == nil {
		return ErrRuntimeInactive
	}

	if run.stopped {
		return ErrRuntimeStopped
	}
	if msg.ID == "" {
		msg.ID = core.NewMessageID()
	}
	run.pending = append(run.pending, msg)
	run.agent.Append(msg)
	return nil
}

// Compact asks the running agent to compact in place using the precomputed
// summary, replacing its conversation chain without tearing the agent down (so
// the system prompt and tools are not rebuilt). The agent records the summary
// and a compaction boundary and emits CompactEvent. Returns false when there is
// no active agent to compact. Safe because the agent applies it at a phase
// boundary on its own goroutine.
func (s *Runtime) Compact(summary string) bool {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	if run == nil {
		return false
	}
	select {
	case run.agent.Inbox() <- core.Inbound{Signal: core.SigCompact, Summary: summary}:
		return true
	case <-run.done:
		return false
	}
}

// interruptDrainTimeout bounds the legacy TUI wait, not event delivery or
// persistence. A slow cooperative tool can outlive this deadline.
const interruptDrainTimeout = 250 * time.Millisecond

// InterruptCurrentTurn requests cancellation synchronously and returns without
// waiting for the captured ThinkAct to unwind. Bind cancellation before scheduling
// any asynchronous wait, so a delayed waiter cannot cancel a successor turn.
//
// The channel observes execution completion only: it can close before TurnEnded
// is emitted, and does not acknowledge outbox consumption or persistence. It does
// not prevent subsequent execution or grant exclusive access to agent state.
// With no runtime it is already closed; between turns the core interrupt latch
// still applies. Neither case proves that an active turn was cancelled.
func (s *Runtime) InterruptCurrentTurn() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.run == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.run.agent.InterruptCurrentTurn()
}

// InterruptTurn retains the TUI's bounded wait while leaving the mailbox alive.
// Callers needing observable execution completion use InterruptCurrentTurn.
func (s *Runtime) InterruptTurn() {
	done := s.InterruptCurrentTurn()
	timer := time.NewTimer(interruptDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (s *Runtime) Outbox() <-chan core.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.outbox
}

func (s *Runtime) PermissionGate() *PermissionGate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.permGate
}

func (s *Runtime) System() core.System {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.run == nil {
		return nil
	}
	return s.run.agent.System()
}

// Tools returns the running agent's toolset, or nil when no agent is active.
// Like System(), this is the toolset actually being sent — built-ins after
// the disabled-tools filter, plus whatever MCP and conditional tools were
// wired in — not a reconstruction of what the params asked for.
func (s *Runtime) Tools() *core.Tools {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.run == nil {
		return nil
	}
	return s.run.agent.Tools()
}

// SetPluginRoot scopes the next agent turn to a plugin. The slash command
// flow calls this when the user invokes a /plugin-skill so subprocesses
// spawned during the turn see PLUGIN_ROOT pointing at that plugin.
// Pass "" to clear (typically done at turn end).
func (s *Runtime) SetPluginRoot(path string) {
	s.mu.Lock()
	s.pluginRoot = path
	s.mu.Unlock()
}

// PluginRoot returns the plugin scope for the current turn, or "" if
// no plugin scope is active.
func (s *Runtime) PluginRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pluginRoot
}
