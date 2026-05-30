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
	// memory_write / skill_manage call and the status-bar target swap
	// ("evolving ⠋ go-testing", "evolving ⠋ memory · debugging", …). The
	// callbacks run on the fork's goroutine; SelfLearnUIState handles its
	// own mutex.
	memStore.SetWriteObserver(func(file string) {
		m.services.SelfLearnUI.Step("memory" + memoryTopicSuffix(file))
	})
	skillMgr.SetWriteObserver(func(_action, name string) {
		m.services.SelfLearnUI.Step(name)
	})

	review := func(kinds selflearn.ReviewKind, snapshot []core.Message) {
		m.services.SelfLearnUI.BeginReview()
		// Tell the main loop to start the spinner tick. Published before the
		// fork runs so the indicator is visible from the first frame.
		m.publishSelfLearnStarted(kinds)
		var (
			summary string
			runErr  error
		)
		defer func() {
			switch {
			case runErr != nil:
				m.services.SelfLearnUI.Fail()
			default:
				m.services.SelfLearnUI.Complete()
			}
		}()

		sys, ok := m.currentSystem()
		if !ok {
			return // session ended between Observe and run; drop silently
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
		summary, runErr = selflearn.RunReview(context.Background(), fc, kinds, snapshot)
		if runErr != nil {
			log.Logger().Warn("self-learning review failed",
				zap.String("kinds", kinds.String()),
				zap.Error(runErr),
			)
			m.publishSelfLearnFailure(kinds, runErr)
			return
		}
		if isNothingToSave(summary) {
			return // §6 invariant #7 — silent on "Nothing to save."
		}
		log.Logger().Info("self-learning review",
			zap.String("kinds", kinds.String()),
			zap.String("summary", summary),
		)
		m.publishSelfLearnSummary(kinds, summary)
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

// currentSystem returns the live agent's System (used by the reviewer fork
// for prefix-cache parity, §6 invariant #2) or false if the session is
// already torn down. We surface only the one thing the ReviewFunc needs
// — keeping the dependency surface narrow avoids the systemOnlyAgent shim
// of an earlier iteration that lied about its capabilities.
func (m *model) currentSystem() (core.System, bool) {
	if !m.services.Agent.Active() {
		return nil, false
	}
	sys := m.services.Agent.System()
	return sys, sys != nil
}

// isNothingToSave matches the model's exit token for a no-op review:
// an empty string or any case/whitespace variant of "Nothing to save."
// The wire-up suppresses the user-visible recap on this branch
// (§6 invariant #7 — silent on "Nothing to save").
func isNothingToSave(summary string) bool {
	s := strings.ToLower(strings.TrimSpace(summary))
	if s == "" {
		return true
	}
	s = strings.TrimSuffix(s, ".")
	return s == "nothing to save"
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

// publishSelfLearnSummary surfaces the just-completed review's one-line
// summary as a hub.Event targeted at "main", which the model loop routes
// through onMainEvent → injectNotification to display in the conversation
// flow. The recap block (§"User-visible surface") is the user's audit
// trail of what L1 just changed. Silent on "Nothing to save" by virtue of
// the caller's empty-summary check.
func (m *model) publishSelfLearnSummary(kinds selflearn.ReviewKind, summary string) {
	if m.agentEventHub == nil {
		return
	}
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.done",
		Source:  "selflearn",
		Target:  "main",
		Subject: fmt.Sprintf("Self-improvement review (%s)", kinds.String()),
		Data:    summary,
	})
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
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.failed",
		Source:  "selflearn",
		Target:  "main",
		Subject: fmt.Sprintf("Self-improvement review failed (%s)", kinds.String()),
		Data:    msg,
	})
}
