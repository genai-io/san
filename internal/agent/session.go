package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/genai-io/gen-code/internal/core"
)

type Task struct {
	mu                 sync.RWMutex
	agent              core.Agent
	permBridge         *PermissionBridge
	cancel             context.CancelFunc
	pendingPermRequest *PermBridgeRequest
	pluginRoot         string // see SetPluginRoot
}

func (s *Task) Start(params BuildParams, messages []core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agent != nil {
		return fmt.Errorf("agent session already active")
	}

	ag, pb, err := buildAgent(params)
	if err != nil {
		return err
	}
	s.agent = ag
	s.permBridge = pb

	if len(messages) > 0 {
		s.agent.SetMessages(messages)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() { _ = s.agent.Run(ctx) }()

	return nil
}

func (s *Task) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Task) stopLocked() {
	if s.agent == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	select {
	case s.agent.Inbox() <- core.Message{Signal: core.SigStop}:
	default:
	}
	s.agent = nil
	s.permBridge = nil
	s.pendingPermRequest = nil
	s.pluginRoot = ""
}

func (s *Task) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agent != nil
}

func (s *Task) Send(content string, images []core.Image) {
	s.mu.RLock()
	ag := s.agent
	s.mu.RUnlock()
	if ag == nil {
		return
	}
	ag.Inbox() <- core.Message{Role: core.RoleUser, Content: content, Images: images}
}

// Compact asks the running agent to compact in place using the precomputed
// summary, replacing its conversation chain without tearing the agent down (so
// the system prompt and tools are not rebuilt). The agent records the summary
// and a compaction boundary and emits CompactEvent. Returns false when there is
// no active agent to compact. Safe because the agent applies it at a phase
// boundary on its own goroutine.
func (s *Task) Compact(summary string) bool {
	s.mu.RLock()
	ag := s.agent
	s.mu.RUnlock()
	if ag == nil {
		return false
	}
	ag.Inbox() <- core.Message{Signal: core.SigCompact, Content: summary}
	return true
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
// dangling *PermBridgeRequest must not survive into the next turn (a
// later SetPendingPermission would then race a stale request against a
// fresh one). The clear runs AFTER the agent quiesces so an in-flight
// PermissionFunc can't repopulate pendingPermRequest via PollPermBridge
// → SetPendingPermission between the clear and the cancel.
func (s *Task) InterruptTurn() {
	s.mu.RLock()
	ag := s.agent
	s.mu.RUnlock()
	if ag == nil {
		return
	}
	done := ag.InterruptCurrentTurn()
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

func (s *Task) Outbox() <-chan core.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.agent == nil {
		return nil
	}
	return s.agent.Outbox()
}

func (s *Task) PermissionBridge() *PermissionBridge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.permBridge
}

func (s *Task) PendingPermission() *PermBridgeRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pendingPermRequest
}

func (s *Task) SetPendingPermission(req *PermBridgeRequest) {
	s.mu.Lock()
	s.pendingPermRequest = req
	s.mu.Unlock()
}

func (s *Task) System() core.System {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.agent == nil {
		return nil
	}
	return s.agent.System()
}

// SetPluginRoot scopes the next agent turn to a plugin. The slash command
// flow calls this when the user invokes a /plugin-skill so subprocesses
// spawned during the turn see PLUGIN_ROOT pointing at that plugin.
// Pass "" to clear (typically done at turn end).
func (s *Task) SetPluginRoot(path string) {
	s.mu.Lock()
	s.pluginRoot = path
	s.mu.Unlock()
}

// PluginRoot returns the plugin scope for the current turn, or "" if
// no plugin scope is active.
func (s *Task) PluginRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pluginRoot
}
