package selflearn

import (
	"context"
	"time"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/tool"
	"github.com/genai-io/gen-code/internal/tool/perm"
)

// forkMaxTurns caps the fork's inference rounds — a review is bounded
// work, so this just stops a confused fork from looping.
const forkMaxTurns = 16

// forkDeadline bounds a single pass. Without it a hung provider would
// pin the goroutine with inFlight=true and silently disable all future
// reviews for the session (§6 invariant #5).
const forkDeadline = 5 * time.Minute

// ForkConfig carries everything RunReview needs to fork a restricted reviewer
// agent. LLM and System come from the parent so the fork inherits its provider
// and (verbatim) system prompt; Memory and Skills are the write surfaces.
type ForkConfig struct {
	LLM    core.LLM
	System core.System // parent's system — read for its prompt only
	CWD    string
	Memory *MemoryStore
	Skills *SkillManager
	// OnEvent receives the fork agent's lifecycle events (PreInfer /
	// PostInfer / OnAppend / …). Wire a sidechain recorder here to land
	// the fork's LLM calls in the main session transcript so the
	// inspector can replay them. nil = no recording (legacy behavior).
	OnEvent func(core.Event)
}

// RunReview forks a restricted agent over the snapshot, runs the kind-
// selected review prompt, and returns the fork's final text (a one-line
// summary, or "nothing to save"). Bounded by forkDeadline. Best-effort:
// the caller must run it on a background goroutine.
func RunReview(ctx context.Context, fc ForkConfig, kinds ReviewKind, snapshot []core.Message) (string, error) {
	prompt := buildReviewPrompt(kinds, fc.CWD, fc.Memory, fc.Skills)

	ctx, cancel := context.WithTimeout(ctx, forkDeadline)
	defer cancel()

	// Fresh System rendering the parent's prompt verbatim (prefix-cache
	// parity). Can't share the parent's instance — NewAgent calls
	// SetObserver on it, which would clobber the parent's telemetry hook.
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
		MaxTurns:  forkMaxTurns,
		OutboxBuf: -1, // no outbox: this fork is headless, driven via ThinkAct
		OnEvent:   fc.OnEvent,
	})

	// Trim trailing user messages so the review prompt isn't the second
	// consecutive user turn — most providers reject that as a role-order
	// violation (the parent's turn may end with a user-role tool_result).
	ag.SetMessages(trimTrailingUserMessages(snapshot))
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

// trimTrailingUserMessages drops trailing user-role messages without
// mutating the caller's slice (it's shared with the main agent).
func trimTrailingUserMessages(msgs []core.Message) []core.Message {
	end := len(msgs)
	for end > 0 && msgs[end-1].Role == core.RoleUser {
		end--
	}
	return msgs[:end]
}

// allowOnly builds a static permission policy: tools in `allowed` pass,
// everything else rejects. No interactive resolver — the fork must never
// prompt the TUI.
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
