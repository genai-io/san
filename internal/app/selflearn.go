// L1 self-learning wire-up. Bridges setting.SelfLearnSettings into a live
// selflearn.Reviewer attached to the current session, and gives it a
// ReviewFunc that forks a restricted reviewer agent against the live LLM +
// System via selflearn.RunReview.
//
// Lifecycle:
//   - wireSelfLearn builds the Reviewer at session start when ≥1 arm is
//     enabled in config; otherwise leaves services.SelfLearn == nil so
//     OnTurnEnd has nothing to do (the §3.1 zero-overhead guarantee).
//   - forwardTurnToSelfLearn is called from OnTurnEnd with the just-completed
//     Result. The Reviewer gates on StopEndTurn internally; we just hand the
//     result through.
//   - clearSelfLearn nils the Reviewer when the session ends so a stale
//     in-flight closure doesn't keep references to a torn-down agent.
//
// See notes/active/l1-background-review.md §9 step 4.
package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/agent"
	"github.com/genai-io/gen-code/internal/app/hub"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/selflearn"
)

// selfLearnDisableEnv is the env-var escape hatch documented in
// notes/active/l1-background-review.md §3.1. Set to "1" / "true" to disable
// the L1 reviewer regardless of settings.json — mirrors Claude Code's
// CLAUDE_CODE_DISABLE_AUTO_MEMORY.
const selfLearnDisableEnv = "GEN_DISABLE_SELF_LEARN"

// wireSelfLearn constructs the L1 Reviewer for the running session, if the
// config has at least one arm enabled. The params snapshot captures the
// provider / model / max-tokens the parent agent was built with — the
// reviewer fork uses the same values so its outbound HTTP request hits the
// same prefix-cache key (§6 invariant #2).
func (m *model) wireSelfLearn(params agent.BuildParams) {
	if m.services.Setting == nil {
		return
	}
	// Env override wins: documented as the hard kill switch (§3.1).
	if v := os.Getenv(selfLearnDisableEnv); v == "1" || strings.EqualFold(v, "true") {
		m.services.SelfLearn = nil
		return
	}
	snap := m.services.Setting.Snapshot()
	resolved, err := selflearn.ResolveSettings(snap.SelfLearn)
	if err != nil {
		log.Logger().Warn("self-learning config rejected at startup", zap.Error(err))
		return
	}
	if !resolved.Config.Enabled() {
		m.services.SelfLearn = nil
		return
	}

	memStore := selflearn.NewMemoryStore(m.env.CWD, resolved.MemoryMaxChars)
	skillMgr := selflearn.NewSkillManager(m.env.CWD, resolved.Perms)

	// The ReviewFunc closes over a getter for the *live* agent so a session
	// rebuild between trigger time and fork time picks up the new System;
	// the LLM client is rebuilt from params to keep the fork independent of
	// the main agent's request cycle. It also drives the user-visible
	// surface (§"User-visible surface"):
	//   - Set/clear services.SelfLearnRunning so renderModeStatus can show
	//     the "evolving" status-bar text while the fork is in flight.
	//   - Publish a hub.Event with the completion summary so the main
	//     event loop surfaces a notice + content block in the conversation
	//     flow ("Nothing to save" stays silent).
	// Wire per-write observers so the UI state picks up each successful
	// memory_write / skill_manage call. The action verbs feed both the
	// live spinner-tail label and the post-pass recap block formatted by
	// formatRecapBlock. The callbacks run on the fork's goroutine;
	// SelfLearnUIState handles its own mutex.
	memStore.SetWriteObserver(func(action, file string) {
		m.services.SelfLearnUI.RecordAction(ReviewAction{
			Verb:   memoryVerb(action),
			Kind:   "memory",
			Target: "memory" + memoryTopicSuffix(file),
		})
	})
	skillMgr.SetWriteObserver(func(action, name string) {
		m.services.SelfLearnUI.RecordAction(ReviewAction{
			Verb:   skillVerb(action),
			Kind:   "skill",
			Target: name,
		})
	})

	review := func(kinds selflearn.ReviewKind, snapshot []core.Message) {
		m.services.SelfLearnUI.BeginReview()
		// Tell the main loop to start the spinner tick. Published before the
		// fork runs so the indicator is visible from the first frame.
		m.publishSelfLearnStarted(kinds)
		var runErr error
		defer func() {
			if runErr != nil {
				m.services.SelfLearnUI.Fail()
			} else {
				m.services.SelfLearnUI.Complete()
			}
		}()

		if !m.services.Agent.Active() {
			return // session ended between Observe and run; drop silently
		}
		sys := m.services.Agent.System()
		if sys == nil {
			return
		}
		client := llm.NewClient(params.Provider, params.ModelID, params.MaxTokens)
		client.SetThinkingEffort(params.ThinkingEffort)

		fc := selflearn.ForkConfig{
			LLM:    client,
			System: sys,
			CWD:    m.env.CWD,
			Memory: memStore,
			Skills: skillMgr,
		}
		_, runErr = selflearn.RunReview(context.Background(), fc, kinds, snapshot)
		if runErr != nil {
			log.Logger().Warn("self-learning review failed",
				zap.String("kinds", kinds.String()),
				zap.Error(runErr),
			)
			m.publishSelfLearnFailure(kinds, runErr)
			return
		}
		// Drain the action log AFTER Complete so the snapshot the status
		// bar formats during the done-hold still has the change count.
		// Empty log ⇒ no writes ⇒ silent (§6 invariant #7). The action
		// log is authoritative; the model's text reply is not consulted.
		actions := m.services.SelfLearnUI.DrainActions()
		if len(actions) == 0 {
			return
		}
		log.Logger().Info("self-learning review",
			zap.String("kinds", kinds.String()),
			zap.Int("changes", len(actions)),
		)
		m.publishSelfLearnSummary(kinds, actions)
	}

	r := selflearn.New(resolved.Config, review)
	r.SeedTurns(countUserTurns(m.conv.ConvertToProvider()))
	m.services.SelfLearn = r
}

// handleSelflearnTick advances the L1 indicator state (spinner frame +
// done/failed decay) and re-arms the next tick if the indicator is still
// in a non-idle phase. Returns nil when the state is idle so the tick
// loop quiesces cleanly.
func (m *model) handleSelflearnTick() tea.Cmd {
	if m.services.SelfLearnUI == nil {
		return nil
	}
	if !m.services.SelfLearnUI.Tick(time.Now()) {
		return nil
	}
	return scheduleSelflearnTick()
}

// memoryTopicSuffix turns the file name reported by MemoryStore into the
// status-bar tail rendered after the "memory" label. "" or the index file
// produce no suffix; topic files like "debugging.md" become " · debugging".
func memoryTopicSuffix(file string) string {
	file = strings.TrimSuffix(file, ".md")
	if file == "" || file == "MEMORY" || file == "memory" {
		return ""
	}
	return " · " + file
}

// countUserTurns counts the user messages in a preloaded history so the
// memory arm's counter resumes on the right cadence beat after session
// restore (invariant #8 hydrate). Skips system / tool messages.
func countUserTurns(msgs []core.Message) int {
	n := 0
	for _, msg := range msgs {
		if msg.Role == core.RoleUser {
			n++
		}
	}
	return n
}

// publishSelfLearnSummary surfaces the just-completed review's recap block
// in the conversation flow. The block is built from the action log (the
// actual tool calls), not the model's narration, so it reads as a
// structural audit trail rather than a self-report.
//
// We pack the whole recap into Subject (which becomes a Notice — display
// only) rather than Data (which injectNotification re-submits to the LLM
// as a fresh user turn — would re-prompt the agent with its own audit
// trail and break the §6 out-of-band promise).
func (m *model) publishSelfLearnSummary(kinds selflearn.ReviewKind, actions []ReviewAction) {
	if m.agentEventHub == nil || len(actions) == 0 {
		return
	}
	header := fmt.Sprintf("Self-improvement review (%s)", kinds.String())
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.done",
		Source:  "selflearn",
		Target:  "main",
		Subject: header + "\n" + formatRecapBlock(actions),
	})
}

// formatRecapBlock builds the delimiter-bounded multi-line recap from the
// pass's action log. Layout matches the design doc's §"User-visible surface"
// example. Empty input ⇒ "" so callers can skip the publish on a no-write
// pass.
func formatRecapBlock(actions []ReviewAction) string {
	if len(actions) == 0 {
		return ""
	}
	const rule = "─────────────────────────────────────────────────"
	var b strings.Builder
	b.WriteString(rule)
	b.WriteString("\n")
	for _, a := range actions {
		fmt.Fprintf(&b, "  · %s %s   %s\n", a.Verb, a.Kind, a.Target)
	}
	b.WriteString(rule)
	return b.String()
}

// memoryVerb maps a memory_write action to the recap-line verb.
func memoryVerb(action string) string {
	switch action {
	case "add":
		return "saved"
	case "replace":
		return "replaced"
	case "remove":
		return "removed"
	default:
		return action
	}
}

// skillVerb maps a skill_manage action to the recap-line verb. patch / edit
// collapse to "updated" because the distinction matters at write time but
// not in a recap; write_file / remove_file describe support-file edits.
func skillVerb(action string) string {
	switch action {
	case "create":
		return "created"
	case "patch", "edit":
		return "updated"
	case "write_file":
		return "extended"
	case "remove_file":
		return "trimmed"
	case "delete":
		return "retired"
	default:
		return action
	}
}

// publishSelfLearnStarted fires a small hub event the instant a review fork
// is about to run. The main loop reacts by scheduling the first spinner
// tick, so the "evolving ⠋" indicator is visible from frame one without
// the render path having to poll for state changes.
func (m *model) publishSelfLearnStarted(kinds selflearn.ReviewKind) {
	if m.agentEventHub == nil {
		return
	}
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.started",
		Source:  "selflearn",
		Target:  "main",
		Subject: kinds.String(),
	})
}

// publishSelfLearnFailure surfaces a best-effort failure notice. We do not
// swallow review errors silently because they indicate something the user
// may want to know (config issue, provider outage, hung fork). The message
// stays terse — full details are in the log per the failure branch of the
// ReviewFunc above.
func (m *model) publishSelfLearnFailure(kinds selflearn.ReviewKind, err error) {
	if m.agentEventHub == nil {
		return
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "review failed (see log)"
	}
	// Subject only — Data routes through SubmitToAgent (see
	// publishSelfLearnSummary docstring for the trap).
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.failed",
		Source:  "selflearn",
		Target:  "main",
		Subject: fmt.Sprintf("Self-improvement review failed (%s): %s", kinds.String(), msg),
	})
}
