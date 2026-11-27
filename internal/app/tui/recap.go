// Recap: coming back to the terminal after being away, one note on where the
// session stands. San states what it measures itself (what waits on you, plan
// progress, the mission, work in the background); a side request writes the
// rest in a sentence or two: the goal, where the work stands, and the one next
// step.
package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/infra/log"
	"github.com/genai-io/san/internal/reflect/recap"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

const recapTimeout = 2 * time.Minute

// recapCacheWindow is how soon after the terminal loses focus a recap is
// written ahead, so it reads the conversation from a still-warm prompt cache
// (Anthropic's default lasts five minutes) rather than paying for all of it
// on return.
const recapCacheWindow = 4 * time.Minute

// recapState is the recap written for an absence and when it shows.
type recapState struct {
	busy      bool          // a recap request is in flight
	show      bool          // show the recap in flight as soon as it lands; implies busy
	ready     recapReadyMsg // a recap written ahead, not shown yet
	requestID string        // fences results from a retired request
	cancel    context.CancelFunc
	pending   *session.Completion
}

type recapPrepareMsg struct{ awaySince time.Time }

type recapReadyMsg struct {
	requestID string
	sessionID string
	chainLen  int // chain length it was written at; a longer chain makes it stale
	text      string
	err       error
	owner     *session.Session
	recording *session.Completion
}

// onBlur starts the clock on an absence. Only a conversation with new ground
// schedules a warm-cache recap; a turn finished while away can still prepare
// one.
func (m *model) onBlur() tea.Cmd {
	m.unfocusedSince = time.Now()
	cfg := m.features.Setting.Recap()
	if !m.shouldAutoRecap(cfg) {
		return nil
	}
	since := m.unfocusedSince
	return tea.Tick(min(cfg.AwayTime(), recapCacheWindow), func(time.Time) tea.Msg { return recapPrepareMsg{since} })
}

// onFocus ends an absence: the input hint skipped meanwhile is fetched, and
// an absence long enough gets its recap.
func (m *model) onFocus() tea.Cmd {
	since := m.unfocusedSince
	m.unfocusedSince = time.Time{}
	return tea.Batch(m.owedSuggestion(), m.recapOnReturn(since))
}

// recapOnReturn recaps an absence that began at since, if it lasted long
// enough and the session is idle. Returning mid-turn skips this automatic
// recap; it must not interrupt tool calls or appear unexpectedly later.
func (m *model) recapOnReturn(since time.Time) tea.Cmd {
	cfg := m.features.Setting.Recap()
	if since.IsZero() || time.Since(since) < cfg.AwayTime() ||
		m.conv.Stream.Active || !m.shouldAutoRecap(cfg) {
		return nil
	}
	return m.deliverRecap()
}

// recapNow answers /recap: a recap of the session as it stands.
func (m *model) recapNow() tea.Cmd {
	if m.env.LLMProvider == nil {
		m.conv.AddNotice("No model connected.")
		return nil
	}
	if !m.canRecap() {
		m.conv.AddNotice("Nothing to recap yet.")
		return nil
	}
	return m.deliverRecap()
}

// deliverRecap shows the recap written ahead when the conversation has not
// moved since, else writes one now, drawn live until it lands. Writing now is
// no dearer than ahead: a conversation that moved has just warmed the cache.
func (m *model) deliverRecap() tea.Cmd {
	if m.recapReady() {
		return m.showRecap(m.recap.ready)
	}
	// Discard stale cached scopes here, not in the read-only readiness predicate.
	m.clearRecapCache()
	// A running spinner already ticks; a second chain would double its speed.
	var tick tea.Cmd
	if !m.needsSpinner() {
		tick = m.conv.Spinner.Tick
	}
	m.recap.show = true
	if m.recap.busy {
		return tick
	}
	return tea.Batch(tick, m.writeRecap())
}

// prepareRecap runs when the cache window comes up or a turn goes idle.
// Write ahead to use the warm cache, but don't show the result early.
func (m *model) prepareRecap() tea.Cmd {
	cfg := m.features.Setting.Recap()
	if m.unfocusedSince.IsZero() || time.Since(m.unfocusedSince) < min(cfg.AwayTime(), recapCacheWindow) ||
		m.recap.busy || !m.shouldAutoRecap(cfg) || !m.sessionQuiet() || m.recapReady() {
		return nil
	}
	return m.writeRecap()
}

// recapReady reports a recap written ahead that the conversation has not
// moved past.
func (m *model) recapReady() bool {
	r := m.recap.ready
	return r.text != "" && r.sessionID == m.sessionID() && r.chainLen == m.chainLen() &&
		r.owner == m.features.Session && (r.owner == nil || r.owner.Err() == nil)
}

// clearRecapCache resolves an unshown result against its original writer.
func (m *model) clearRecapCache() {
	closeAside(m.recap.ready.recording)
	m.recap.ready = recapReadyMsg{}
}

// resetRecap retires cached and in-flight work before its owner is replaced.
func (m *model) resetRecap() {
	if m.recap.cancel != nil {
		m.recap.cancel()
	}
	closeAside(m.recap.pending)
	m.clearRecapCache()
	m.recap = recapState{}
}

// canRecap reports whether there is a conversation to recap.
func (m *model) canRecap() bool {
	return m.env.LLMProvider != nil && !m.conv.Compact.Active && m.assistantReplies() > 0
}

// shouldAutoRecap gates every unasked recap: the feature is on, the session has
// new ground to cover and the user is not already typing. New ground is read
// from the recorded recap notice rather than process-local state, so a resumed
// session does not recap the same completed work again.
func (m *model) shouldAutoRecap(cfg setting.RecapSettings) bool {
	owner := m.features.Session
	if cfg.AfterMinutes < 0 || owner == nil || !m.canRecap() || m.hasDraft() {
		return false
	}
	history := owner.Snapshot()
	return history.UserTurnsSinceNotice("/recap ") >= cfg.Turns
}

// hasDraft reports a draft in the composer: the user is already back in the
// conversation, so an unasked recap would only interrupt.
func (m *model) hasDraft() bool { return strings.TrimSpace(m.userInput.FullValue()) != "" }

// sessionQuiet reports whether the conversation is holding still: no turn at
// work, unless it waits on the user.
func (m *model) sessionQuiet() bool {
	return !m.conv.Stream.Active || m.waitingOn() != ""
}

// recapLive draws the recap being written for a user who is back.
func (m *model) recapLive() string {
	if !m.recap.show {
		return ""
	}
	return chat.RenderRecap(widgets.LiveRecap(m.conv.Spinner.Frame()), m.recapFacts(), "", m.env.Width)
}

func (m *model) writeRecap() tea.Cmd {
	m.clearRecapCache()
	m.recap.busy = true
	req := m.newAsideRequest(session.PurposeRecap, true, recap.Instructions(m.recapFacts(), m.recapGoal()))
	ctx, cancel := context.WithTimeout(context.Background(), recapTimeout)
	m.recap.requestID, m.recap.cancel, m.recap.pending = core.NewMessageID(), cancel, req.snapshot.Recording()
	ready := recapReadyMsg{requestID: m.recap.requestID, sessionID: m.sessionID(), chainLen: req.chainLen, owner: req.owner, recording: req.snapshot.Recording()}
	return func() tea.Msg {
		defer cancel()
		ready.text, ready.err = recap.Generate(ctx, req.complete)
		return ready
	}
}

func (m *model) onRecapReady(msg recapReadyMsg) tea.Cmd {
	if msg.requestID != m.recap.requestID {
		closeAside(msg.recording)
		return nil
	}
	m.recap.pending, m.recap.cancel = nil, nil
	m.recap.busy = false
	show := m.recap.show
	m.recap.show = false
	if msg.sessionID != m.sessionID() || msg.owner != m.features.Session ||
		(msg.owner != nil && msg.owner.Err() != nil) || msg.chainLen != m.chainLen() {
		closeAside(msg.recording)
		return nil
	}
	if msg.err != nil || msg.text == "" {
		closeAside(msg.recording)
		log.Logger().Debug("recap not written", zap.Error(msg.err))
		if show {
			m.conv.AddNotice("Couldn't write a recap.")
		}
		return nil
	}
	if show {
		return m.showRecap(msg)
	}
	m.clearRecapCache()
	m.recap.ready = msg
	cfg := m.features.Setting.Recap()
	if !m.unfocusedSince.IsZero() && time.Since(m.unfocusedSince) >= cfg.AwayTime() &&
		m.recapReady() && m.sessionQuiet() && m.shouldAutoRecap(cfg) {
		return m.showRecap(msg)
	}
	return nil
}

// showRecap adds the recap to the conversation as a notice the agent never
// sees. The facts are measured now, not when the text was written.
func (m *model) showRecap(r recapReadyMsg) tea.Cmd {
	if m.recap.ready.recording != r.recording {
		m.clearRecapCache()
	}
	m.recap.ready = recapReadyMsg{}
	if r.recording != nil && (r.sessionID != m.sessionID() || r.owner != m.features.Session || r.chainLen != m.chainLen()) {
		closeAside(r.recording)
		return nil
	}
	notice := chat.RecapNotice(m.recapFacts(), r.text)
	if r.recording != nil && !finishAside(r.recording, notice) {
		return nil
	}
	if r.recording == nil && r.owner != nil {
		if r.owner != m.features.Session || r.sessionID != m.sessionID() || r.chainLen != m.chainLen() {
			return nil
		}
		if err := r.owner.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: notice}}); err != nil {
			log.Logger().Error("record recap notice", zap.Error(err))
			return nil
		}
	}
	if r.recording != nil || r.owner != nil {
		// Already persisted through the captured owner; do not invoke OnNotice again.
		m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatNotice, Content: notice})
	} else {
		m.conv.AddNotice(notice)
	}
	return tea.Batch(m.CommitMessages()...)
}

// recapGoal is the first line of the mission AutoPilot drives; "" leaves the
// goal to the model.
func (m *model) recapGoal() string {
	line, _, _ := strings.Cut(m.drivenMission(), "\n")
	return strings.TrimSpace(line)
}

// recapFacts is what San knows without asking the model, most urgent first:
// what waits on the user, the plan's progress, the mission and the work running
// in the background.
func (m *model) recapFacts() string {
	var facts []string
	if needed := m.waitingOn(); needed != "" {
		facts = append(facts, needed+" needed")
	}
	if done, plans := chat.PlanProgress(m.features.Tracker.List()); plans > 0 {
		facts = append(facts, "plan "+strconv.Itoa(done)+"/"+strconv.Itoa(plans))
	}
	if ap := m.env.AutoPilot; m.drivenMission() != "" {
		state := string(ap.MissionState)
		if state == "" {
			state = "ready"
		}
		mission := "mission " + state
		if ap.MissionState == setting.MissionRunning && ap.Steers.TurnEnd {
			mission += " " + m.continuationCount()
		}
		facts = append(facts, mission)
	}
	if n := len(m.features.Task.ListRunning()); n > 0 {
		facts = append(facts, strconv.Itoa(n)+" in background")
	}
	return strings.Join(facts, " · ")
}

// waitingOn names what an open prompt waits for from the user, "" when
// nothing does.
func (m *model) waitingOn() string {
	ov, ok := m.activeOverlay()
	if !ok {
		return ""
	}
	switch ov.(type) {
	case *input.ApprovalModel:
		return "approval"
	case *chat.QuestionPrompt:
		return "answer"
	case *input.SecretPromptModel:
		return "input"
	}
	return ""
}

// chainLen is the length of the agent's chain: what a recap was written from.
func (m *model) chainLen() int { return len(m.features.Agent.Messages()) }
