package agent

import (
	"context"

	"github.com/genai-io/san/internal/hook"
)

// TurnCallbacks bridge normal completion to the host's state and input sources.
// Callbacks run synchronously on the host's event owner.
type TurnCallbacks struct {
	RecordState func() error
	NextInput   func() bool
	AutoPilot   func() bool
	Idle        func()
}

// EndTurn records state before admitting a successor. Cancellation and runtime
// failure have separate cleanup paths and must not call EndTurn.
func EndTurn(c TurnCallbacks) error {
	if err := c.RecordState(); err != nil {
		return err
	}
	if c.NextInput() || c.AutoPilot() {
		return nil
	}
	c.Idle()
	return nil
}

// StopCheck snapshots the hook input on the event owner before asynchronous work.
type StopCheck struct {
	hooks    *hook.Engine
	input    hook.HookInput
	hasHooks bool
}

func NewStopCheck(hooks *hook.Engine, lastAssistant string) StopCheck {
	check := StopCheck{hooks: hooks, input: hook.HookInput{LastAssistantMessage: lastAssistant}}
	if hooks != nil {
		check.hasHooks = hooks.HasHooks(hook.Stop)
		check.input.StopHookActive = hooks.StopHookActive()
	}
	return check
}

type StopDecision struct {
	Blocked bool
	Reason  string
}

// Run leaves timeout, fail-open and audit policy with the hook engine.
func (c StopCheck) Run(ctx context.Context) StopDecision {
	if c.hooks == nil {
		return StopDecision{}
	}
	var outcome hook.HookOutcome
	if c.hasHooks {
		outcome = c.hooks.Execute(ctx, hook.Stop, c.input)
	}
	c.hooks.ExecuteAsync(hook.Notification, hook.HookInput{
		Message: "Claude is waiting for your input", NotificationType: "idle_prompt",
	})
	return StopDecision{Blocked: outcome.ShouldBlock, Reason: outcome.BlockReason}
}

// Finish records the post-hook state before either reporting completion or
// admitting a hook continuation. The host must reject stale decisions first.
func (d StopDecision) Finish(recordState func() error) (continuation string, err error) {
	if err := recordState(); err != nil {
		return "", err
	}
	if d.Blocked {
		return "Stop hook blocked: " + d.Reason, nil
	}
	return "", nil
}
