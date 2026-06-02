// L1 self-learning wire-up: bridges setting.SelfLearnSettings into a
// session-scoped selflearn.Reviewer + ReviewFunc that forks against the
// live LLM/System via selflearn.RunReview.
// See notes/active/l1-background-review.md §9 step 4.
package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/agent"
	"github.com/genai-io/gen-code/internal/app/hub"
	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/selflearn"
)

// selfLearnDisableEnv is the env kill switch (§3.1) — mirrors Claude
// Code's CLAUDE_CODE_DISABLE_AUTO_MEMORY.
const selfLearnDisableEnv = "GEN_DISABLE_SELF_LEARN"

// wireSelfLearn builds the L1 Reviewer for the running session when ≥1
// arm is enabled. params is captured so the fork rebuilds an LLM client
// with the same provider/model/max-tokens for prefix-cache parity
// (§6 invariant #2).
func (m *model) wireSelfLearn(params agent.BuildParams) {
	// Tear down first — ensureAgentSession can re-enter via an agent
	// toggle (which calls Agent.Stop directly, bypassing StopAgentSession)
	// and would otherwise overwrite reviewCancel un-called, leaking the
	// context and pinning the old fork for up to forkDeadline.
	m.teardownSelfLearn()

	if m.services.Setting == nil {
		return
	}
	// Env override wins: documented as the hard kill switch (§3.1).
	if v := os.Getenv(selfLearnDisableEnv); v == "1" || strings.EqualFold(v, "true") {
		m.services.SelfLearn.Reviewer = nil
		return
	}
	snap := m.services.Setting.Snapshot()
	cfg, err := selflearn.ResolveSettings(snap.SelfLearn)
	if err != nil {
		log.Logger().Warn("self-learning config rejected at startup", zap.Error(err))
		return
	}
	if !cfg.Enabled() {
		m.services.SelfLearn.Reviewer = nil
		return
	}

	// Session-scoped review context. StopAgentSession's clearSelfLearn calls
	// the cancel so an in-flight fork unblocks immediately on /clear / quit
	// instead of waiting up to forkDeadline for its independent timeout.
	reviewCtx, reviewCancel := context.WithCancel(context.Background())
	m.services.SelfLearn.Cancel = reviewCancel

	// live gates the fork-goroutine write observers below. They capture this
	// local (not m.services.SelfLearn) so they never race on a services field
	// the UI goroutine mutates; teardownSelfLearn flips it false.
	live := &atomic.Bool{}
	live.Store(true)
	m.services.SelfLearn.Live = live

	memStore := selflearn.NewMemoryStore(m.env.CWD, cfg.MemoryMaxChars)
	skillMgr := selflearn.NewSkillManager(m.env.CWD, cfg.Perms)

	// Write observers feed the live spinner-tail and the post-pass recap.
	// They run on the fork goroutine and check `live` so writes landing
	// after teardown drop silently instead of racing on UI state.
	memStore.SetWriteObserver(func(action, file, note string) {
		if !live.Load() {
			return
		}
		m.services.SelfLearn.Indicator.RecordAction(ReviewAction{
			Verb:   memoryVerb(action),
			Kind:   "memory",
			Target: memoryTopicName(file),
			Note:   note,
		})
	})
	skillMgr.SetWriteObserver(func(action, name, note string) {
		if !live.Load() {
			return
		}
		m.services.SelfLearn.Indicator.RecordAction(ReviewAction{
			Verb:   skillVerb(action),
			Kind:   "skill",
			Target: name,
			Note:   note,
		})
	})

	review := func(kinds selflearn.ReviewKind, snapshot []core.Message) {
		// Liveness check before any UI mutation — a teardown race must
		// not flash "evolving → evolved" on a session the user just killed.
		if !m.services.Agent.Active() {
			return
		}
		sys := m.services.Agent.System()
		if sys == nil {
			return
		}

		m.services.SelfLearn.Indicator.BeginReview()
		m.publishSelfLearnStarted(kinds)

		client := llm.NewClient(params.Provider, params.ModelID, params.MaxTokens)
		client.SetThinkingEffort(params.ThinkingEffort)
		// Sidechain recorder: the fork's LLM calls land in the main
		// session's transcript marked as sidechain so the inspector can
		// surface them under the "selflearn-review" agent without
		// interleaving them into the main conversation thread.
		var forkOnEvent func(core.Event)
		if rec := m.services.Session.NewSidechainRecorder("selflearn-review", params.Provider.Name(), params.ModelID, params.MaxTokens); rec != nil {
			forkOnEvent = rec.OnAgentEvent
		}
		fc := selflearn.ForkConfig{
			LLM:     client,
			System:  sys,
			CWD:     m.env.CWD,
			Memory:  memStore,
			Skills:  skillMgr,
			OnEvent: forkOnEvent,
		}
		llmSummary, runErr := selflearn.RunReview(reviewCtx, fc, kinds, snapshot)
		if runErr != nil {
			m.services.SelfLearn.Indicator.Fail()
			log.Logger().Warn("self-learning review failed",
				zap.String("kinds", kinds.String()),
				zap.Error(runErr),
			)
			m.publishSelfLearnFailure(kinds, runErr)
			return
		}
		// Complete BEFORE Drain so doneCount snapshots len(s.actions);
		// zero-write pass collapses to idle inside Complete (§6 #7).
		// The reviewer's last line ("trimmed go-testing SKILL.md by
		// 1.8KB") becomes the done-phase status tag; the action-log
		// fallback covers a misbehaving / silent reviewer.
		m.services.SelfLearn.Indicator.Complete(llmSummary)
		actions := m.services.SelfLearn.Indicator.DrainActions()
		if len(actions) == 0 {
			return
		}
		log.Logger().Info("self-learning review",
			zap.String("kinds", kinds.String()),
			zap.Int("changes", len(actions)),
		)
		m.publishSelfLearnSummary(kinds, actions)
	}

	r := selflearn.New(cfg, review)
	r.SeedTurns(countUserTurns(m.conv.Messages))
	m.services.SelfLearn.Reviewer = r
}

// runSelfLearnDemo drives the indicator through one scripted lifecycle
// (reviewing → 3 actions → done) so a developer can eyeball the spinner /
// target / done-summary in a real terminal without firing a live LLM
// review. Returns immediately; the script runs on a background goroutine.
func (m *model) runSelfLearnDemo() {
	ind := m.services.SelfLearn.Indicator
	if ind == nil {
		return
	}
	const kinds = selflearn.KindMemory | selflearn.KindSkills
	go func() {
		ind.BeginReview()
		m.publishSelfLearnStarted(kinds)

		steps := []struct {
			wait   time.Duration
			action ReviewAction
		}{
			{800 * time.Millisecond, ReviewAction{
				Verb: "saved", Kind: "memory", Target: "",
				Note: "noted that lint runs via make ci, not go vet",
			}},
			{1200 * time.Millisecond, ReviewAction{
				Verb: "saved", Kind: "memory", Target: "debugging",
				Note: "added 3 race-condition repro tips",
			}},
			{1200 * time.Millisecond, ReviewAction{
				Verb: "updated", Kind: "skill", Target: "go-testing",
				Note: "trimmed verbose examples, kept the table-test snippet",
			}},
			{1200 * time.Millisecond, ReviewAction{
				Verb: "created", Kind: "skill", Target: "python-typing",
				Note: "new skill, typing-hints and Protocol patterns",
			}},
		}
		for _, s := range steps {
			time.Sleep(s.wait)
			ind.RecordAction(s.action)
		}
		time.Sleep(800 * time.Millisecond)
		ind.Complete("trimmed go-testing SKILL.md by 1.8KB · saved 2 notes")
		actions := ind.DrainActions()
		m.publishSelfLearnSummary(kinds, actions)
	}()
}

// teardownSelfLearn unwires the current L1 reviewer: cancels the
// session-scoped fork context, marks the wiring dead, and drops the
// Reviewer. Idempotent. Called from StopAgentSession and the top of
// wireSelfLearn so a rebuild never leaks the prior context.
func (m *model) teardownSelfLearn() {
	if cancel := m.services.SelfLearn.Cancel; cancel != nil {
		cancel()
	}
	m.services.SelfLearn.Cancel = nil
	if live := m.services.SelfLearn.Live; live != nil {
		live.Store(false)
	}
	m.services.SelfLearn.Live = nil
	m.services.SelfLearn.Reviewer = nil
}

// handleSelflearnTick advances the indicator and schedules the next tick
// at the cadence Tick returns (spinner interval while reviewing; one
// deadline tick during done/failed hold). Returns nil when idle.
func (m *model) handleSelflearnTick() tea.Cmd {
	if m.services.SelfLearn.Indicator == nil {
		return nil
	}
	delay, stillActive := m.services.SelfLearn.Indicator.Tick(time.Now())
	if !stillActive {
		return nil
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return selflearnTickMsg{} })
}

// memoryTopicName returns the bare topic name (e.g. "debugging") for a
// memory file, or "" for the index. The indicator renderer adds the
// "memory" / "memory · " prefix at display time.
func memoryTopicName(file string) string {
	file = strings.TrimSuffix(file, ".md")
	if file == "" || file == "MEMORY" || file == "memory" {
		return ""
	}
	return file
}

// countUserTurns counts user messages so the memory arm resumes on the
// right cadence beat after session restore (§6 invariant #8).
func countUserTurns(msgs []core.ChatMessage) int {
	n := 0
	for _, msg := range msgs {
		if msg.Role == core.RoleUser {
			n++
		}
	}
	return n
}

// publishSelfLearnSummary posts the post-pass recap into the conversation
// flow. Recap goes in Subject (display-only Notice); routing it through
// Data would re-submit it to the LLM and break the §6 out-of-band promise.
func (m *model) publishSelfLearnSummary(kinds selflearn.ReviewKind, actions []ReviewAction) {
	if m.agentEventHub == nil || len(actions) == 0 {
		return
	}
	_ = kinds // header dropped; recap is self-evident
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.done",
		Source:  "selflearn",
		Target:  "main",
		Subject: formatRecapBlock(actions),
	})
}

// formatRecapBlock renders the post-review recap as a three-level
// italic-dim block so the hierarchy reads cleanly:
//
//	Self-improvement
//	  memory
//	    · index — noted lint runs via make ci, not go vet
//	    · debugging — added 3 race-condition repro tips
//	  skill
//	    · go-testing — trimmed verbose examples
//	    · python-typing — new skill, typing-hints
//
// Actions are grouped by Kind (preserving first-seen order); a bare
// target in the memory group renders as "index" so the rows stay
// aligned. Empty input ⇒ "" so callers can skip the publish on a
// no-write pass — the dialog never appears for silent reviews.
func formatRecapBlock(actions []ReviewAction) string {
	if len(actions) == 0 {
		return ""
	}
	// Group by Kind, preserving first-seen order.
	type group struct {
		kind string
		rows []ReviewAction
	}
	var groups []group
	idx := map[string]int{}
	for _, a := range actions {
		if i, ok := idx[a.Kind]; ok {
			groups[i].rows = append(groups[i].rows, a)
		} else {
			idx[a.Kind] = len(groups)
			groups = append(groups, group{kind: a.Kind, rows: []ReviewAction{a}})
		}
	}

	var b strings.Builder
	b.WriteString(selflearnRecapHeaderStyle.Render("Self-improvement"))
	for _, g := range groups {
		b.WriteString("\n")
		b.WriteString(selflearnRecapKindStyle.Render("  " + g.kind))
		for _, a := range g.rows {
			b.WriteString("\n")
			b.WriteString(selflearnRecapRowStyle.Render(recapRow(a)))
		}
	}
	return b.String()
}

// recapRow formats one action row inside a kind group, e.g.
//
//	"    · debugging — added 3 race-condition repro tips"
//
// Memory writes without a topic (the index file) render as "index" so
// every row in the group lines up under the same column.
func recapRow(a ReviewAction) string {
	target := a.Target
	if target == "" && a.Kind == "memory" {
		target = "index"
	}
	row := "    · " + target
	if note := strings.TrimSpace(a.Note); note != "" {
		row += " — " + note
	}
	return row
}

// selflearnRecap*Style — italic + dim across the block so it reads as
// a quiet background thought, never competing with the chat content.
// Kind sub-headers carry no extra weight; the indent does the work.
var (
	selflearnRecapHeaderStyle = lipgloss.NewStyle().
					Foreground(kit.CurrentTheme.TextDim).
					Italic(true)
	selflearnRecapKindStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Italic(true)
	selflearnRecapRowStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Italic(true)
)

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

// skillVerb maps a skill_manage action to its recap verb. patch/edit
// collapse to "updated"; write_file/remove_file are support-file edits.
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

// publishSelfLearnStarted nudges the main loop to schedule the first
// spinner tick, so "evolving ⠋" appears from frame one.
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

// publishSelfLearnFailure surfaces a terse failure notice; full details
// land in the log. Subject only (Data routes through SubmitToAgent —
// see publishSelfLearnSummary).
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
		Subject: fmt.Sprintf("Self-improvement review failed (%s): %s", kinds.String(), msg),
	})
}
