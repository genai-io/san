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
		// Sidechain recorder: each L1 fork gets its OWN session ID
		// (formatted "<parent>.selflearn-review.<unix>") so
		// `gen --resume <fork-id>` replays exactly that review's LLM
		// calls in isolation. The recap row surfaces this fork ID.
		var forkOnEvent func(core.Event)
		var forkSessionID string
		if rec := m.services.Session.NewSidechainRecorder("selflearn-review", params.Provider.Name(), params.ModelID, params.MaxTokens); rec != nil {
			forkOnEvent = rec.OnAgentEvent
			forkSessionID = rec.SessionID()
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
			zap.String("fork-session", forkSessionID),
		)
		m.publishSelfLearnSummary(kinds, actions, forkSessionID)
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
		// Demo: fabricate a plausible-looking fork session ID so the
		// recap footer is identical in shape to the real path.
		demoSessionID := fmt.Sprintf("demo-session.selflearn-review.%d", time.Now().Unix())
		m.publishSelfLearnSummary(kinds, actions, demoSessionID)
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
// forkSessionID points at the L1 fork's own session so the recap can
// suggest "gen --resume <id>" for replay.
func (m *model) publishSelfLearnSummary(kinds selflearn.ReviewKind, actions []ReviewAction, forkSessionID string) {
	if m.agentEventHub == nil || len(actions) == 0 {
		return
	}
	_ = kinds // header dropped; recap is self-evident
	m.agentEventHub.Publish(hub.Event{
		Type:    "selflearn.review.done",
		Source:  "selflearn",
		Target:  "main",
		Subject: formatRecapBlock(actions, forkSessionID),
	})
}

// formatRecapBlock renders the post-review recap as a hand-built box
// so the "gen --resume <id>" hint can ride on the bottom border:
//
//	╭┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╮
//	┊  memory                                       ┊
//	┊    · index — noted that lint runs via make ci ┊
//	┊    · debugging — added 3 race-condition tips  ┊
//	┊  skill                                        ┊
//	┊    · go-testing — trimmed verbose examples    ┊
//	┊    · python-typing — new skill, typing-hints  ┊
//	╰┄ gen --resume demo.selflearn-review.123 ┄┄┄┄┄╯
//
// Actions are grouped by Kind (preserving first-seen order); a bare
// memory target renders as "index" so every row lines up. Empty
// input ⇒ "" so the publish is skipped on no-write passes.
func formatRecapBlock(actions []ReviewAction, sessionID string) string {
	if len(actions) == 0 {
		return ""
	}
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

	// Pre-render each content line; widest determines the box width.
	// A blank line between kind groups gives the eye a moment to rest
	// so the two sections don't read as one long list.
	var lines []string
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, recapKindStyle(g.kind).Render(g.kind))
		for _, a := range g.rows {
			lines = append(lines, recapRowLine(a))
		}
	}
	const gutter = 3 // 3-col side padding instead of 2 — adds visual breathing without spending a vertical row
	contentWidth := 0
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > contentWidth {
			contentWidth = w
		}
	}
	// Footer needs to fit on the bottom border: "╰┄ <text> ┄╯".
	// Layout is corner(1) + leadDash(1) + space(1) + footer + space(1) +
	// trailDash(>=1) + corner(1) = footer + 6 minimum cells across the
	// row. The top border is innerWidth + 2 cells, so the constraint is
	// innerWidth >= footer + 4, i.e. contentWidth >= footer.
	var footerText string
	footerLen := 0
	if sessionID != "" {
		// "↪ " prefix turns the footer from a passive label into an
		// affordance: it reads as "next action" rather than chrome.
		footerText = selflearnRecapFooterStyle.Render("↪ gen --resume " + sessionID)
		footerLen = lipgloss.Width(footerText)
		if footerLen > contentWidth {
			contentWidth = footerLen
		}
	}
	innerWidth := contentWidth + 2*gutter

	border := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#4A4A52", Light: "#C8C8CC"})
	var b strings.Builder
	// Top border: ╭┄┄…┄╮. No vertical padding rows inside — the
	// horizontal gutter alone handles the breathing room and the
	// card stays tight.
	b.WriteString(border.Render("╭" + strings.Repeat("┄", innerWidth) + "╮"))
	for _, ln := range lines {
		pad := contentWidth - lipgloss.Width(ln)
		b.WriteString("\n")
		b.WriteString(border.Render("┊"))
		b.WriteString(strings.Repeat(" ", gutter))
		b.WriteString(ln)
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(strings.Repeat(" ", gutter))
		b.WriteString(border.Render("┊"))
	}
	// Bottom border: ╰┄ <footer> ┄…┄╯  (or ╰┄┄…┄╯ when no footer fits)
	b.WriteString("\n")
	if footerText != "" {
		// Top span between corners = innerWidth.
		// Bottom span between corners = lead(┄) + " " + footer + " " +
		// trail(┄…) = 3 + footerLen + trailDashes.
		// Equal → trailDashes = innerWidth - footerLen - 3.
		trailDashes := max(innerWidth-footerLen-3, 1)
		b.WriteString(border.Render("╰┄"))
		b.WriteString(" ")
		b.WriteString(footerText)
		b.WriteString(" ")
		b.WriteString(border.Render(strings.Repeat("┄", trailDashes) + "╯"))
	} else {
		b.WriteString(border.Render("╰" + strings.Repeat("┄", innerWidth) + "╯"))
	}
	return b.String()
}

// recapRowLine formats one action row: " · <target>" optionally
// followed by " — <note>". Single-space indent so the bullet sits
// directly under the kind sub-header without dragging the column
// further right.
func recapRowLine(a ReviewAction) string {
	target := a.Target
	if target == "" && a.Kind == "memory" {
		target = "index"
	}
	row := " · " + target
	if note := strings.TrimSpace(a.Note); note != "" {
		row += " — " + note
	}
	return selflearnRecapRowStyle.Render(row)
}

// recapKindStyle returns the per-kind sub-header style: blue for
// memory, purple for skill, dim for anything else.
func recapKindStyle(kind string) lipgloss.Style {
	switch kind {
	case "memory":
		return selflearnRecapMemoryStyle
	case "skill":
		return selflearnRecapSkillStyle
	default:
		return selflearnRecapKindStyle
	}
}

// selflearnRecap*Style — the recap sits inside a thin rounded box
// drawn in TextDim so the frame stays soft chrome. Inside, kind
// sub-headers carry the only color (memory blue, skill purple) and
// rows stay italic + TextDim.
var (
	selflearnRecapKindStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Italic(true)
	// Memory blue / skill purple — desaturated ~15-20% vs the previous
	// values so they blend with the overall muted/italic aesthetic
	// instead of pulling focus from chat content.
	selflearnRecapMemoryStyle = lipgloss.NewStyle().
					Foreground(lipgloss.AdaptiveColor{Dark: "#82A0BA", Light: "#487192"}).
					Italic(true)
	selflearnRecapSkillStyle = lipgloss.NewStyle().
					Foreground(lipgloss.AdaptiveColor{Dark: "#A89AC4", Light: "#745783"}).
					Italic(true)
	selflearnRecapRowStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Italic(true)
	// Footer style for "gen --resume <id>" embedded on the bottom border.
	// TextDim + Faint so the command reads as a quiet label baked into
	// the chrome — kept upright (no italic) so the shell command is
	// instantly copy-paste recognisable.
	selflearnRecapFooterStyle = lipgloss.NewStyle().
					Foreground(kit.CurrentTheme.TextDim).
					Faint(true)
	// selflearnLiveStyle dresses the inline indicator row (the spinner
	// + target line that lives above the prompt while a review runs).
	// Italic + TextDim so it sits softly in the chat flow without
	// pulling focus from real messages.
	selflearnLiveStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Italic(true)
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
