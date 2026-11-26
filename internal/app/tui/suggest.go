// Suggest: the next input, predicted after each turn and shown as ghost text
// (Tab accepts it). It is a side request, so it reads the whole conversation
// at the price of a cache hit.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/autopilot"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
)

// startPromptSuggestion fires the input hint. With Suggest off it never runs,
// in or out of AutoPilot mode.
// Under AutoPilot with a mission it proposes the next step toward it;
// otherwise it predicts the human's next input, once the conversation has
// something to go on.
func (m *model) startPromptSuggestion() tea.Cmd {
	if m.env.LLMProvider == nil || !m.env.AutoPilot.Steers.SuggestOn() {
		return nil
	}
	// Nobody reads ghost text in an unfocused terminal; it is fetched on return.
	if !m.unfocusedSince.IsZero() {
		m.suggestOwed = true
		return nil
	}
	mission := m.drivenMission()
	var steering string
	if mission != "" {
		steering = m.autopilotSteeringInstructions()
	} else if m.assistantReplies() < 2 {
		return nil
	}
	prompt, details := autopilot.SuggestionPrompt(steering, mission)
	req := m.newAsideRequest(session.PurposeSuggest, true, prompt, details)
	return m.userInput.PromptSuggestion.Start(func(ctx context.Context) (string, error) {
		resp, err := req.complete(ctx)
		if err != nil {
			closeAside(req.snapshot.Recording())
			return "", err
		}
		if finishErr := req.snapshot.Recording().Finish(context.WithoutCancel(ctx)); finishErr != nil {
			return "", finishErr
		}
		return resp.Content.Text(), nil
	})
}

// owedSuggestion fetches the hint skipped while the terminal was unfocused,
// unless a turn is at work: its end fetches one anyway.
func (m *model) owedSuggestion() tea.Cmd {
	owed := m.suggestOwed
	m.suggestOwed = false
	if !owed || m.conv.Stream.Active {
		return nil
	}
	return m.startPromptSuggestion()
}

// drivenMission is the live mission while AutoPilot drives, else "": what
// Suggest proposes the next step toward and Recap states as the goal. Read
// fresh every call, so clearing the mission takes effect immediately.
func (m *model) drivenMission() string {
	if !m.autopilotEngaged() {
		return ""
	}
	return strings.TrimSpace(m.env.AutoPilot.ActiveMission())
}

// assistantReplies counts the assistant's messages in the conversation.
func (m *model) assistantReplies() int {
	n := 0
	for _, msg := range m.conv.Messages {
		if msg.Role == core.ChatAssistant {
			n++
		}
	}
	return n
}
