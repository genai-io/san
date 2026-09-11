package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/genai-io/san/internal/core"
)

var (
	ErrSessionInactive = errors.New("agent session is not active")
	ErrSessionStopped  = errors.New("agent session stopped before the message was accepted")
)

type sessionRun struct {
	agent  core.Agent
	cancel context.CancelFunc
	done   chan struct{}
}

type agentBuilder func(BuildParams) (core.Agent, *PermissionGate, error)

type Session struct {
	mu                 sync.RWMutex
	run                *sessionRun
	permGate           *PermissionGate
	pendingPermRequest *PermGateRequest
	pluginRoot         string // see SetPluginRoot
	lastRunErr         error
	build              agentBuilder
}

func (s *Session) Start(params BuildParams, messages []core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.run != nil {
		return fmt.Errorf("agent session already active")
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

	ctx, cancel := context.WithCancel(context.Background())
	run := &sessionRun{agent: ag, cancel: cancel, done: make(chan struct{})}
	s.run = run
	s.permGate = pg
	s.lastRunErr = nil
	go s.execute(run, ctx)

	return nil
}

func (s *Session) execute(run *sessionRun, ctx context.Context) {
	err := run.agent.Run(ctx)

	s.mu.Lock()
	if s.run == run {
		s.lastRunErr = err
		s.clearRunLocked()
	}
	close(run.done)
	s.mu.Unlock()
}

const sessionStopTimeout = 2 * time.Second

// Stop performs a bounded graceful shutdown. Callers that need a different
// deadline should use StopContext.
func (s *Session) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionStopTimeout)
	defer cancel()
	return s.StopContext(ctx)
}

// StopContext cancels the active run and waits for that exact generation to
// finish. The run remains active until Run returns, so a concurrent Start can
// never overlap the previous agent's teardown.
func (s *Session) StopContext(ctx context.Context) error {
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

func (s *Session) clearRunLocked() {
	s.run = nil
	s.permGate = nil
	s.pendingPermRequest = nil
	s.pluginRoot = ""
}

func (s *Session) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.run != nil
}

// Messages returns a snapshot of the running agent's conversation chain, or nil
// when no agent is active. This is the authoritative context the agent is
// actually sending to the model — used to carry history into a rebuilt agent so
// a mid-conversation rebuild never drops the conversation.
func (s *Session) Messages() []core.Message {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	if run == nil {
		return nil
	}
	return run.agent.Messages()
}

// Send delivers an already-identified message to the active agent. Callers
// should create the Message once at the input boundary and reuse its ID for
// the TUI projection and transcript.
func (s *Session) Send(ctx context.Context, msg core.Message) error {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	if run == nil {
		return ErrSessionInactive
	}
	if msg.ID == "" {
		msg.ID = core.NewMessageID()
	}

	select {
	case <-run.done:
		return ErrSessionStopped
	default:
	}
	select {
	case run.agent.Inbox() <- core.Inbound{Msg: msg}:
		select {
		case <-run.done:
			return ErrSessionStopped
		default:
			return nil
		}
	case <-run.done:
		return ErrSessionStopped
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Compact asks the running agent to compact in place using the precomputed
// summary, replacing its conversation chain without tearing the agent down (so
// the system prompt and tools are not rebuilt). The agent records the summary
// and a compaction boundary and emits CompactEvent. Returns false when there is
// no active agent to compact. Safe because the agent applies it at a phase
// boundary on its own goroutine.
func (s *Session) Compact(summary string) bool {
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

// interruptDrainTimeout caps how long InterruptTurn waits for the agent
// goroutine to actually unwind its in-flight ThinkAct. Keeping this
// tight avoids UI stalls; if the agent is still in a slow tool the
// caller proceeds anyway — provider-side convert layers strip any
// orphaned tool_use blocks before the next inference fires.
const interruptDrainTimeout = 250 * time.Millisecond

// InterruptTurn cancels the agent's in-flight turn without ending its
// Run loop and waits briefly for the turn to actually unwind. The next
// Send goes through the same inbox channel and resumes the session in
// place — no rebuild, no Stop/Start event pair.
//
// Also clears pendingPermRequest: a permission prompt that was open at
// the moment of interrupt is dropped along with the turn, so the
// dangling *PermGateRequest must not survive into the next turn (a
// later SetPendingPermission would then race a stale request against a
// fresh one). The clear runs AFTER the agent quiesces so an in-flight
// PermissionFunc can't repopulate pendingPermRequest via PollPermGate
// → SetPendingPermission between the clear and the cancel.
func (s *Session) InterruptTurn() {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	if run == nil {
		return
	}
	done := run.agent.InterruptCurrentTurn()
	timer := time.NewTimer(interruptDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	s.mu.Lock()
	s.pendingPermRequest = nil
	s.mu.Unlock()
}

func (s *Session) Outbox() <-chan core.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.run == nil {
		return nil
	}
	return s.run.agent.Outbox()
}

func (s *Session) PermissionGate() *PermissionGate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.permGate
}

func (s *Session) PendingPermission() *PermGateRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pendingPermRequest
}

func (s *Session) SetPendingPermission(req *PermGateRequest) {
	s.mu.Lock()
	s.pendingPermRequest = req
	s.mu.Unlock()
}

func (s *Session) System() core.System {
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
func (s *Session) Tools() core.Tools {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.run == nil {
		return nil
	}
	return s.run.agent.Tools()
}

// LastRunError returns the most recent Run result. It is primarily useful for
// diagnostics after an unexpected runner exit; Start clears it.
func (s *Session) LastRunError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRunErr
}

// SetPluginRoot scopes the next agent turn to a plugin. The slash command
// flow calls this when the user invokes a /plugin-skill so subprocesses
// spawned during the turn see PLUGIN_ROOT pointing at that plugin.
// Pass "" to clear (typically done at turn end).
func (s *Session) SetPluginRoot(path string) {
	s.mu.Lock()
	s.pluginRoot = path
	s.mu.Unlock()
}

// PluginRoot returns the plugin scope for the current turn, or "" if
// no plugin scope is active.
func (s *Session) PluginRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pluginRoot
}
