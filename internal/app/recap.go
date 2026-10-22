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

const recapInstructions = `<system-reminder>The user stepped away from this session and is coming back. Write them a recap. You are a separate, one-off note: the main work is not interrupted, and you cannot use tools.
Plain text, no markdown, in the language of the conversation, under 25 words in total, one short line each:
%GOAL%Now: what is being worked on and where it stands, in a few words (name the file, PR or test).
Next: the one next action. Begin it with "Your turn:" when it waits on the user.
No reasons, internals or secondary to-dos.%FACTS%
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

// onFocus ends an absence: one long enough gets its recap, the one written
// ahead if the conversation has not moved since, else a fresh one.
func (m *model) onFocus() tea.Cmd {
	since := m.recap.awaySince
	m.recap.awaySince = time.Time{}
	after := m.env.AutoPilot.RecapAfter()
	if since.IsZero() || after == 0 || time.Since(since) < after || !m.recapWanted() {
		return nil
	}
	m.recap.away = time.Since(since)
	if r := m.recap.ready; r.text != "" && r.sessionID == m.sessionID() && r.chainLen == m.chainLen() {
		return m.showRecap(r)
	}
	m.recap.show = true
	if m.recap.busy {
		return nil
	}
	return m.writeRecap()
}

// recapNow answers /recap: a recap of the session as it stands.
func (m *model) recapNow() tea.Cmd {
	if m.env.LLMProvider == nil || m.assistantReplies() == 0 {
		m.conv.AddNotice("Nothing to recap yet.")
		return nil
	}
	m.recap.away = 0
	if r := m.recap.ready; r.text != "" && r.sessionID == m.sessionID() && r.chainLen == m.chainLen() {
		return m.showRecap(r)
	}
	m.recap.show = true
	if m.recap.busy {
		return nil
	}
	return m.writeRecap()
}

// prepareRecap writes the recap ahead while the user is away and the
// conversation is quiet, so it reads a warm cache and is ready on return.
// Called when the cache window comes up and when a turn ends after it.
func (m *model) prepareRecap() tea.Cmd {
	if m.recap.awaySince.IsZero() || m.recap.busy || m.env.AutoPilot.RecapAfter() == 0 || !m.recapWanted() {
		return nil
	}
	n := m.chainLen()
	if n == m.recap.shownAt || (m.recap.ready.text != "" && m.recap.ready.chainLen == n) {
		return nil
	}
	return m.writeRecap()
}

// prepareRecapAtTurnEnd rewrites the recap after a turn that ran while the
// user is away, once the cache window has come up: the chain just moved, and
// the cache is as warm as it gets.
func (m *model) prepareRecapAtTurnEnd() tea.Cmd {
	after := m.env.AutoPilot.RecapAfter()
	if m.recap.awaySince.IsZero() || after == 0 || time.Since(m.recap.awaySince) < min(after, recapCacheWindow) {
		return nil
	}
	return m.prepareRecap()
}

// recapWanted reports whether there is something to recap that the screen
// does not already show: not a turn at work (unless it waits on the user), not
// a draft the user left mid-thought.
func (m *model) recapWanted() bool {
	if m.env.LLMProvider == nil || m.conv.Compact.Active || m.assistantReplies() == 0 {
		return false
	}
	if m.conv.Stream.Active && m.waitingOn() == "" {
		return false
	}
	return strings.TrimSpace(m.userInput.FullValue()) == ""
}

func (m *model) writeRecap() tea.Cmd {
	m.recap.busy = true
	goal := m.recapGoal()
	prompt := strings.ReplaceAll(recapInstructions, "%GOAL%", "Goal: the session's overall goal, in a few words.\n")
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
