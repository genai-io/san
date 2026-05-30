package selflearn

import (
	"context"
	"time"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/tool"
	"github.com/genai-io/gen-code/internal/tool/perm"
)

// DefaultMaxTurns caps the reviewer fork's inference rounds. A review is a small
// bounded task (read the turn, write at most a few entries); the cap stops a
// confused fork from looping.
const DefaultMaxTurns = 16

// DefaultForkDeadline is the wall-clock cap on a single review pass. The
// design's invariant #5 is "best-effort" — if the fork hangs (slow provider,
// stuck tool call), an indefinite block would leave inFlight=true and
// silently disable all future reviews for the session (see Observe's drop
// path). The deadline guarantees the goroutine returns and clears inFlight.
const DefaultForkDeadline = 5 * time.Minute

// ForkConfig carries everything RunReview needs to fork a restricted reviewer
// agent. LLM and System come from the parent so the fork inherits its provider
// and (verbatim) system prompt; Memory and Skills are the write surfaces.
type ForkConfig struct {
	LLM      core.LLM
	System   core.System // parent's system — read for its prompt only
	CWD      string
	Memory   *MemoryStore
	Skills   *SkillManager
	MaxTurns int           // 0 = DefaultMaxTurns
	Deadline time.Duration // 0 = DefaultForkDeadline
}

// RunReview forks a restricted agent over the turn snapshot and runs the review
// prompt selected by kinds. It returns the fork's final text (a one-line summary
// of what it changed, or a "nothing to save" note). Best-effort: the caller runs
// it on a background goroutine and never lets its error affect the user turn.
//
// The fork runs under a wall-clock deadline (fc.Deadline or DefaultForkDeadline)
// so a hung provider call can't leave the goroutine pinned and the reviewer's
// inFlight flag stuck (invariant #5 / #8).
func RunReview(ctx context.Context, fc ForkConfig, kinds ReviewKind, snapshot []core.Message) (string, error) {
	prompt := buildReviewPrompt(kinds, fc.CWD, fc.Skills)

	maxTurns := fc.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	deadline := fc.Deadline
	if deadline <= 0 {
		deadline = DefaultForkDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	// Fresh System that renders the parent's prompt verbatim (prefix-cache
	// parity) without sharing the parent's System instance — core.NewAgent calls
	// SetObserver on the System it is given, which would clobber the parent's
	// telemetry observer if we passed the parent's System directly.
	parentPrompt := ""
	if fc.System != nil {
		parentPrompt = fc.System.Prompt()
	}
	sys := core.NewSystem()
	sys.Use(core.Section{
		Slot:   core.SlotIdentity,
		Name:   "inherited-system",
		Source: core.Injected,
		Render: func() string { return parentPrompt },
	}, "selflearn")

	tools := core.NewTools(newMemoryWriteTool(fc.Memory), newSkillManageTool(fc.Skills))
	restricted := tool.WithPermission(tools, allowOnly(tools))

	ag := core.NewAgent(core.Config{
		LLM:       fc.LLM,
		System:    sys,
		Tools:     restricted,
		AgentType: "selflearn-review",
		CWD:       fc.CWD,
		MaxTurns:  maxTurns,
		OutboxBuf: -1, // no outbox: this fork is headless, driven via ThinkAct
	})

	ag.SetMessages(snapshot)
	ag.Append(ctx, core.UserMessage(prompt, nil))
	res, err := ag.ThinkAct(ctx)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", nil
	}
	return res.Content, nil
}

// allowOnly builds a permission policy that allows exactly the tools in the
// given registry and rejects everything else. The reviewer fork must never
// prompt the TUI, so the policy is static and self-contained — there is no
// interactive resolver and no escalation path.
func allowOnly(allowed core.Tools) perm.PermissionFunc {
	names := make(map[string]bool)
	for _, t := range allowed.All() {
		names[t.Name()] = true
	}
	return func(_ context.Context, name string, _ map[string]any) (bool, string) {
		if names[name] {
			return true, ""
		}
		return false, "tool not permitted for the self-learning reviewer"
	}
}
