package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

var (
	ErrRuntimeInactive = errors.New("agent session is not active")
	ErrRuntimeStopped  = errors.New("agent session stopped before the message was accepted")
)

type run struct {
	agent     core.Agent
	cancel    context.CancelFunc
	done      chan struct{}
	pending   []core.Message // accepted inputs not yet announced by MessageAdded; Runtime.mu guards
	stopped   bool
	onEvent   func(core.Event)
	recording *session.Session
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
	run := &run{done: make(chan struct{}), onEvent: params.OnEvent, recording: params.Recording}
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
	run.cancel() // release finalization waiters even after a natural mailbox stop

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

// Stop cancels the whole active run and waits for that exact generation to
// finish. The run remains active until Run returns, so a concurrent Start can
// never overlap the previous agent's teardown. The caller owns the deadline;
// on failure the run and conversation remain available for a later stop.
func (s *Runtime) Stop(ctx context.Context) error {
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
func (s *Runtime) Reset(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil {
		return fmt.Errorf("agent session restarted during reset")
	}
	s.messages = nil
	s.outbox = nil
	return nil
}

// Send delivers a message to the active agent. Callers create the Message once
// at the input boundary so the TUI projection, the inbox, and the transcript
// share one ID; a message without one is stamped on acceptance.
func (s *Runtime) Send(msg core.Message) error {
	return s.SendInput(PendingInput{Message: msg})
}

// SendInput commits a recorded input before infallible native queue admission.
func (s *Runtime) SendInput(input PendingInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.run
	if run == nil {
		return ErrRuntimeInactive
	}
	if run.stopped {
		return ErrRuntimeStopped
	}
	msg := input.Message
	if msg.ID == "" {
		msg.ID = core.NewMessageID()
	}
	if input.Recording != nil {
		if !input.Recording.OwnedBy(run.recording) {
			return &llm.RecordingError{Err: fmt.Errorf("input completion belongs to another session")}
		}
		if err := input.Recording.FinishMessage(context.Background(), msg); err != nil {
			return &llm.RecordingError{Err: err}
		}
	}
	run.pending = append(run.pending, msg)
	run.agent.Append(msg)
	return nil
}

// Compact transfers a precomputed result to the live mailbox. False with no
// error means there is no runtime; an error leaves ownership with the caller.
func (s *Runtime) Compact(result core.Compaction) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run := s.run
	if run == nil {
		return false, nil
	}
	if run.stopped {
		return false, ErrRuntimeStopped
	}
	if !run.agent.Compact(result) {
		return false, fmt.Errorf("agent is not accepting compaction")
	}
	return true, nil
}

// Cancel cancels only the current turn, keeping the runtime active for later input.
// It returns without waiting for execution or the event consumer. The receipt stays
// bound to the captured turn across runtime replacement; nil means no runtime or
// no unfinished exchange was captured.
func (s *Runtime) Cancel() *core.TurnFinalization {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.run == nil {
		return nil
	}
	return s.run.agent.Cancel()
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
