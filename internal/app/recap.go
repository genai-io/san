// Recap: coming back to the terminal after being away, one note on where the
// session stands. San states what it measures itself (time away, what waits on
// you, plan progress, the mission, work in the background); a side request
// writes the rest: the goal, what is being done now, and the one next step.
package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/todo"
)

const recapTimeout = 2 * time.Minute

// recapCacheWindow is how soon after the terminal loses focus a recap is
// written ahead, so it reads the conversation from a still-warm prompt cache
// (Anthropic's default lasts five minutes) rather than paying for all of it
// on return.
const recapCacheWindow = 4 * time.Minute

const recapInstructions = `<system-reminder>The user stepped away and is coming back. Write a recap that lets them pick the work back up at a glance. You are a one-off note beside the conversation: the main work is not interrupted, and you cannot use tools.
Plain text, no markdown, in the language of the conversation, under 40 words, one line each:
%GOAL%Now: where the work stands: what is done, what is in progress, anything failed or blocked. Name the file, PR or test.
Next: the one next action. Begin it with "Your turn:" when it waits on the user.
Skip reasons, internals and side to-dos.%FACTS%
</system-reminder>`

// recapState follows the terminal's focus and the recap written for it.
type recapState struct {
	awaySince time.Time     // when the terminal lost focus; zero while it has it
	away      time.Duration // how long the absence being recapped lasted
	busy      bool          // a recap request is in flight
	show      bool          // show the recap in flight as soon as it lands
	ready     recapReadyMsg // a recap written ahead, not shown yet
	shownAt   int           // chain length the last shown recap was written at
}

type recapPrepareMsg struct{ awaySince time.Time }

type recapReadyMsg struct {
	sessionID string
	chainLen  int    // chain length it was written at; a longer chain makes it stale
	goal      string // the mission it stood on, stated by San, not the model
	text      string
	err       error
}

// onBlur starts the clock on an absence and schedules writing its recap
// ahead, inside the cache window.
func (m *model) onBlur() tea.Cmd {
	m.recap.awaySince = time.Now()
	after := m.env.AutoPilot.RecapAfter()
	if after == 0 {
		return nil
	}
	since := m.recap.awaySince
	return tea.Tick(min(after, recapCacheWindow), func(time.Time) tea.Msg { return recapPrepareMsg{since} })
}

// onFocus ends an absence: the input hint skipped meanwhile is fetched, and
// an absence long enough gets its recap.
func (m *model) onFocus() tea.Cmd {
	since := m.recap.awaySince
	m.recap.awaySince = time.Time{}
	return tea.Batch(m.owedSuggestion(), m.recapOnReturn(since))
}

// recapOnReturn recaps an absence that began at since, if it lasted long
// enough: the one written ahead if the conversation has not moved since, else
// a fresh one.
func (m *model) recapOnReturn(since time.Time) tea.Cmd {
	after := m.env.AutoPilot.RecapAfter()
	if since.IsZero() || after == 0 || time.Since(since) < after || !m.recapWanted() || m.hasDraft() {
		return nil
	}
	m.recap.away = time.Since(since)
	return m.deliverRecap()
}

// recapNow answers /recap: a recap of the session as it stands.
func (m *model) recapNow() tea.Cmd {
	if m.env.LLMProvider == nil {
		m.conv.AddNotice("No model connected.")
		return nil
	}
	if !m.recapWanted() {
		m.conv.AddNotice("Nothing to recap yet.")
		return nil
	}
	m.recap.away = 0
	return m.deliverRecap()
}

// deliverRecap shows the recap written ahead when the conversation has not
// moved since, else writes one now, drawn live until it lands. Writing now is
// no dearer than ahead: a conversation that moved has just warmed the cache.
func (m *model) deliverRecap() tea.Cmd {
	if r := m.recap.ready; r.text != "" && r.sessionID == m.sessionID() && r.chainLen == m.chainLen() {
		return m.showRecap(r)
	}
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

// prepareRecap writes the recap ahead while the user is away and the
// conversation is quiet, so it reads the cache before it expires and is ready
// on return. A session at work keeps its cache warm, so it is left to be
// written on return. Called when the cache window comes up and when the
// session goes idle after it.
func (m *model) prepareRecap() tea.Cmd {
	if m.recap.awaySince.IsZero() || m.recap.busy || m.env.AutoPilot.RecapAfter() == 0 || !m.recapWanted() || m.hasDraft() || !m.sessionQuiet() {
		return nil
	}
	n := m.chainLen()
	if n == m.recap.shownAt || (m.recap.ready.text != "" && m.recap.ready.chainLen == n) {
		return nil
	}
	return m.writeRecap()
}

// prepareRecapAtTurnEnd rewrites the recap when the session goes idle while
// the user is away, once the cache window has come up: the chain just moved,
// and the cache is as warm as it gets.
func (m *model) prepareRecapAtTurnEnd() tea.Cmd {
	after := m.env.AutoPilot.RecapAfter()
	if m.recap.awaySince.IsZero() || after == 0 || time.Since(m.recap.awaySince) < min(after, recapCacheWindow) {
		return nil
	}
	return m.prepareRecap()
}

// recapWanted reports whether there is a conversation to recap.
func (m *model) recapWanted() bool {
	return m.env.LLMProvider != nil && !m.conv.Compact.Active && m.assistantReplies() > 0
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
	if !m.recap.show || !m.recap.busy {
		return ""
	}
	return conv.RenderRecap(m.recapFacts(m.recap.away), m.conv.Spinner.View()+" writing…")
}

func (m *model) writeRecap() tea.Cmd {
	m.recap.busy = true
	goal := m.recapGoal()
	prompt := strings.ReplaceAll(recapInstructions, "%GOAL%", "Goal: what the session is for, in a few words.\n")
	if goal != "" {
		prompt = strings.ReplaceAll(recapInstructions, "%GOAL%", "")
	}
	facts := ""
	if f := m.recapFacts(0); f != "" {
		facts = "\nThe user already sees these facts; don't repeat them: " + f + "."
	}
	if goal != "" {
		facts += "\nThe goal is shown to them already: " + goal
	}
	req := m.newSideRequest("recap", strings.ReplaceAll(prompt, "%FACTS%", facts))
	ready := recapReadyMsg{sessionID: m.sessionID(), chainLen: len(req.chain), goal: goal}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), recapTimeout)
		defer cancel()
		resp, err := req.complete(ctx)
		ready.text, ready.err = strings.TrimSpace(resp.Content.Text()), err
		return ready
	}
}

func (m *model) onRecapReady(msg recapReadyMsg) tea.Cmd {
	m.recap.busy = false
	show := m.recap.show
	m.recap.show = false
	if msg.sessionID != m.sessionID() {
		return nil
	}
	if msg.err != nil || msg.text == "" {
		log.Logger().Debug("recap not written", zap.Error(msg.err))
		if show {
			m.conv.AddNotice("Couldn't write a recap.")
		}
		return nil
	}
	if !show {
		m.recap.ready = msg
		return nil
	}
	return m.showRecap(msg)
}

// showRecap adds the recap to the conversation as a notice the agent never
// sees. The facts are measured now, not when the text was written.
func (m *model) showRecap(r recapReadyMsg) tea.Cmd {
	m.recap.ready = recapReadyMsg{}
	m.recap.shownAt = r.chainLen
	body := r.text
	if r.goal != "" {
		body = "Goal: " + r.goal + "\n" + body
	}
	m.conv.AddNotice(conv.RecapNotice(m.recapFacts(m.recap.away), body))
	return tea.Batch(m.CommitMessages()...)
}

// recapGoal is the mission AutoPilot drives, on one line; "" leaves the goal
// to the model.
func (m *model) recapGoal() string {
	mission := m.drivenMission()
	line, _, _ := strings.Cut(mission, "\n")
	return kit.TruncateText(strings.TrimSpace(line), 60)
}

// recapFacts is what San knows without asking the model, most urgent first:
// time away, what waits on the user, the plan's progress, the mission and the
// work running in the background.
func (m *model) recapFacts(away time.Duration) string {
	var facts []string
	if away > 0 {
		facts = append(facts, "away "+formatAway(away))
	}
	if needed := m.waitingOn(); needed != "" {
		facts = append(facts, needed+" needed")
	}
	plans, done := 0, 0
	for _, t := range m.services.Tracker.List() {
		if todo.BackgroundTaskID(t) == "" {
			plans++
			if t.Status == todo.StatusCompleted && !todo.EndedAbnormally(t) {
				done++
			}
		}
	}
	if plans > 0 {
		facts = append(facts, "plan "+strconv.Itoa(done)+"/"+strconv.Itoa(plans))
	}
	if ap := m.env.AutoPilot; m.drivenMission() != "" {
		state := string(ap.MissionState)
		if state == "" {
			state = "ready"
		}
		mission := "mission " + state
		if ap.MissionState == setting.MissionRunning && ap.Steers.TurnEnd {
			mission += " " + strconv.Itoa(m.autopilotContinuations)
			if !ap.ContinuationsUnlimited() {
				mission += "/" + strconv.Itoa(ap.ResolvedMaxContinuations())
			}
		}
		facts = append(facts, mission)
	}
	if n := len(m.services.Task.ListRunning()); n > 0 {
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
	case *conv.QuestionPrompt:
		return "answer"
	case *input.SecretPromptModel:
		return "input"
	}
	return ""
}

// chainLen is the length of the agent's chain: what a recap was written from.
func (m *model) chainLen() int { return len(m.services.Agent.Messages()) }

// formatAway renders an absence as "23m" or "2h05m".
func formatAway(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes < 60 {
		return strconv.Itoa(minutes) + "m"
	}
	m := minutes % 60
	pad := ""
	if m < 10 {
		pad = "0"
	}
	return strconv.Itoa(minutes/60) + "h" + pad + strconv.Itoa(m) + "m"
}
