// L1 self-learning wire-up: bridges setting.SelfLearnSettings into a
// session-scoped selflearn.Reviewer + ReviewFunc that forks against the
// live LLM/System via selflearn.RunReview.
// See notes/active/l1-background-review.md §9 step 4.
package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.uber.org/zap"

	"github.com/genai-io/san/v2/internal/agent"
	"github.com/genai-io/san/v2/internal/app/hub"
	"github.com/genai-io/san/v2/internal/app/input"
	"github.com/genai-io/san/v2/internal/app/kit"
	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/core/system"
	"github.com/genai-io/san/v2/internal/llm"
	"github.com/genai-io/san/v2/internal/log"
	"github.com/genai-io/san/v2/internal/selflearn"
	"github.com/genai-io/san/v2/internal/setting"
)

// selfLearnDisableEnvSuffix is the env kill switch (§3.1) — mirrors Claude
// Code's CLAUDE_CODE_DISABLE_AUTO_MEMORY. Read via setting.Getenv as
// SAN_DISABLE_SELF_LEARN.
const selfLearnDisableEnvSuffix = "DISABLE_SELF_LEARN"

// L1 review lifecycle event types published on agentEventHub. "started" is
// an internal spinner wake-up consumed by onMainEvent; "done"/"failed"
// surface as user-visible notices.
const (
	eventSelfLearnReviewStarted = "selflearn.review.started"
	eventSelfLearnReviewDone    = "selflearn.review.done"
	eventSelfLearnReviewFailed  = "selflearn.review.failed"
)

// wireSelfLearn builds the L1 Reviewer for the running session when ≥1
// arm is enabled. params is captured so the fork rebuilds an LLM client
// with the same provider/model/max-tokens for prefix-cache parity
// (§6 invariant #2). pendingSend is the user content the caller is about
// to deliver — it is already in m.conv.Messages but has NOT been Observed
// yet, so SeedTurns must exclude it to keep the cadence beat honest.
func (m *model) wireSelfLearn(params agent.BuildParams, pendingSend string) {
	// Tear down first — ensureAgentSession can re-enter via an agent
	// toggle (which calls Agent.Stop directly, bypassing StopAgentSession)
	// and would otherwise overwrite reviewCancel un-called, leaking the
	// context and pinning the old fork for up to forkDeadline.
	m.teardownSelfLearn()

	if m.services.Setting == nil {
		return
	}
	// Env override wins: documented as the hard kill switch (§3.1).
	if v := setting.Getenv(selfLearnDisableEnvSuffix); v == "1" || strings.EqualFold(v, "true") {
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
	// after teardown drop silently instead of racing on UI state. The
	// memory and skill paths are mechanically identical — same gate,
	// same indicator hop — so they share one record helper.
	record := func(kind string, verb func(string) string, target func(string) string) func(action, key, note string) {
		return func(action, key, note string) {
			if !live.Load() {
				return
			}
			m.services.SelfLearn.Indicator.RecordAction(ReviewAction{
				Verb:   verb(action),
				Kind:   kind,
				Target: target(key),
				Note:   note,
			})
		}
	}
	identity := func(s string) string { return s }
	memStore.SetWriteObserver(record("memory", memoryVerb, memoryTopicName))
	skillMgr.SetWriteObserver(record("skill", skillVerb, identity))

	review := func(kinds selflearn.ReviewKind, snapshot []core.Message) {
		// Liveness checks before any UI mutation — a teardown race must
		// not flash "evolving → evolved" on a session the user just killed.
		// Active() guards the macro window; the per-phase live.Load() checks
		// below catch a teardown that lands AFTER Active() returned true.
		if !m.services.Agent.Active() {
			return
		}
		sys := m.services.Agent.System()
		if sys == nil {
			return
		}

		if !live.Load() {
			return
		}
		m.services.SelfLearn.Indicator.BeginReview()
		m.publishSelfLearnStarted(kinds)

		client := llm.NewClient(params.Provider, params.ModelID, params.MaxTokens)
		client.SetThinkingEffort(params.ThinkingEffort)
		// Sidechain recorder: each L1 fork gets its OWN session ID
		// (formatted "<parent>.selflearn-review.<unix>") so
		// `san --resume <fork-id>` replays exactly that review's LLM
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
		// Re-check live AFTER the RunReview return. This is the macro
		// window — RunReview can sit on the LLM for up to forkDeadline
		// and teardown is most likely to have landed by now.
		if !live.Load() {
			return
		}
		if runErr != nil {
			m.services.SelfLearn.Indicator.Fail()
			// Drain even on failure so a partial pass (e.g. two memory
			// writes that succeeded before the LLM timed out) still
			// reaches the recap — otherwise the user sees "review
			// failed" with no record of what was actually persisted.
			actions := m.services.SelfLearn.Indicator.DrainActions()
			log.Logger().Warn("self-learning review failed",
				zap.String("kinds", kinds.String()),
				zap.Int("partial-changes", len(actions)),
				zap.Error(runErr),
			)
			m.publishSelfLearnFailure(kinds, runErr)
			if len(actions) > 0 {
				m.publishSelfLearnSummary(actions, forkSessionID)
			}
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
		m.publishSelfLearnSummary(actions, forkSessionID)
	}

	r := selflearn.New(cfg, review)
	r.SeedTurns(countUserTurns(m.conv.Messages, pendingSend))
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
		m.publishSelfLearnSummary(actions, demoSessionID)
	}()
}

// notifySelfLearnOverride detects when a /config save was silently
// overridden by the OTHER settings level (the merger ORs the Enabled
// flags across user+project so disabling at one level cannot turn off an
// arm that the other level enabled). Surfaces a notice so the user
// learns about the override instead of seeing a "saved" confirmation
// while the behavior stays unchanged.
func (m *model) notifySelfLearnOverride(msg input.ConfigSavedMsg) {
	if m.services.Setting == nil {
		return
	}
	snap := m.services.Setting.Snapshot()
	if snap == nil {
		return
	}
	var arms []string
	if msg.SavedSelfLearn.Memory.Enabled != snap.SelfLearn.Memory.Enabled {
		arms = append(arms, "Memory")
	}
	if msg.SavedSelfLearn.Skills.Enabled != snap.SelfLearn.Skills.Enabled {
		arms = append(arms, "Skills")
	}
	if len(arms) == 0 {
		return
	}
	other := "project"
	if msg.Scope == "project" {
		other = "user"
	}
	m.conv.AddNotice("Note: " + strings.Join(arms, " and ") +
		" enabled state is overridden by " + other + "-level settings")
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
	if file == "" || strings.EqualFold(file, system.AutoMemoryIndexName) {
		return ""
	}
	return strings.TrimSuffix(file, ".md")
}

// countUserTurns counts user messages already Observed by the reviewer
// so the memory arm resumes on the right cadence beat after session
// restore (§6 invariant #8). A trailing pendingSend match is excluded —
// the submit path Appends the message before ensureAgentSession runs, so
// without this guard the seed double-counts the in-flight turn that
// Observe is about to increment.
func countUserTurns(msgs []core.ChatMessage, pendingSend string) int {
	end := len(msgs)
	if pendingSend != "" && end > 0 {
		last := msgs[end-1]
		if last.Role == core.RoleUser && last.Content == pendingSend {
			end--
		}
	}
	n := 0
	for i := 0; i < end; i++ {
		if msgs[i].Role == core.RoleUser {
			n++
		}
	}
	return n
}

// publishSelfLearnSummary posts the post-pass recap into the conversation
// flow. Recap goes in Subject (display-only Notice); routing it through
// Data would re-submit it to the LLM and break the §6 out-of-band promise.
// forkSessionID points at the L1 fork's own session so the recap can
// suggest "san --resume <id>" for replay.
func (m *model) publishSelfLearnSummary(actions []ReviewAction, forkSessionID string) {
	if m.agentEventHub == nil || len(actions) == 0 {
		return
	}
	m.agentEventHub.Publish(hub.Event{
		Type:    eventSelfLearnReviewDone,
		Source:  "selflearn",
		Target:  "main",
		Subject: formatRecapBlock(actions, forkSessionID),
	})
}

// formatRecapBlock renders the post-review recap as a lipgloss-bordered
// card with the "san --resume" hint as a separate line below — no more
// hand-built width math, no footer crammed into the bottom border.
// Layout:
//
//	╭┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╮
//	┊  memory                                       ┊
//	┊    · index — noted that lint runs via make ci ┊
//	┊    · debugging — added 3 race-condition tips  ┊
//	┊                                               ┊
//	┊  skill                                        ┊
//	┊    · go-testing — trimmed verbose examples    ┊
//	┊    · python-typing — new skill, typing-hints  ┊
//	╰┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╯
//	  ↪ san --resume demo-session.selflearn-review.123
//
// Empty input ⇒ "" so the publish is skipped on no-write passes.
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

	var inner strings.Builder
	for gi, g := range groups {
		if gi > 0 {
			inner.WriteString("\n\n") // blank line between groups
		}
		inner.WriteString(recapKindStyle(g.kind).Render(g.kind))
		for _, a := range g.rows {
			inner.WriteString("\n")
			inner.WriteString(recapRowLine(a))
		}
	}

	out := selflearnRecapBoxStyle.Render(inner.String())
	if sessionID != "" {
		// "↪ " prefix flips the line from passive label to affordance.
		// Indented to match the box's left padding so the arrow lines
		// up under the first content column.
		out += "\n  " + selflearnRecapFooterStyle.Render("↪ san --resume "+sessionID)
	}
	return out
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
	// Box style — lipgloss-managed dashed border, soft TextDim corners.
	// Padding(0, 2) is the standard 2-col gutter inside the frame; the
	// card stays compact because there are no vertical padding rows.
	selflearnRecapBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{
			Top:         "┄",
			Bottom:      "┄",
			Left:        "┊",
			Right:       "┊",
			TopLeft:     "╭",
			TopRight:    "╮",
			BottomLeft:  "╰",
			BottomRight: "╯",
		}).
		BorderForeground(lipgloss.AdaptiveColor{Dark: "#4A4A52", Light: "#C8C8CC"}).
		Padding(0, 2)
	// Footer style for "↪ san --resume <id>" on its own line below the
	// box. TextDim + Faint so the command reads as a quiet hint, kept
	// upright so the shell command stays copy-paste recognisable.
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
		Type:    eventSelfLearnReviewStarted,
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
		Type:    eventSelfLearnReviewFailed,
		Source:  "selflearn",
		Target:  "main",
		Subject: fmt.Sprintf("Self-improvement review failed (%s): %s", kinds.String(), msg),
	})
}
