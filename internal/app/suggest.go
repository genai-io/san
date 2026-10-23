// Suggest: the next input, predicted after each turn and shown as ghost text
// (Tab accepts it). It is a side request, so it reads the whole conversation
// at the price of a cache hit.
package app

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
)

const suggestNextInput = `<system-reminder>Suggestion mode: predict what the user will type next in this session. You are a separate, one-off note: the main work is not interrupted, and you cannot use tools.
Predict what THEY would type, not what you think they should do: would they think "I was just about to type that"? Be specific: "run the tests" beats "continue".
Never suggest evaluations ("looks good"), questions, your own voice ("Let me…"), new ideas they didn't ask about, or more than one sentence.
Reply with nothing if the next step isn't obvious, or right after an error or a misunderstanding: let them assess it.
Reply with ONLY the suggestion, 2-12 words in the user's language and style: no quotes, no explanation.
</system-reminder>`

// suggestTowardMission asks for the next step of a mission AutoPilot drives,
// weighed by the copilot's steering instructions.
const suggestTowardMission = `<system-reminder>Suggestion mode: propose the SINGLE next instruction to give the agent toward the mission below. You are a separate, one-off note: the main work is not interrupted, and you cannot use tools.
Reply with ONLY that instruction, a short direct imperative the user can accept and send as-is (a few words to one sentence): no quotes, no explanation. Reply with nothing if the mission looks complete or the next step needs a human decision.
</system-reminder>`

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
	prompt := suggestNextInput
	if mission := m.drivenMission(); mission != "" {
		prompt = suggestTowardMission + "\n\n<steering_instructions>\n" + m.autopilotSteeringInstructions() +
			"\n</steering_instructions>\n\nMission:\n" + mission
	} else if m.assistantReplies() < 2 {
		return nil
	}
	req := m.newSideRequest("suggest", prompt)
	return m.userInput.PromptSuggestion.Start(func(ctx context.Context) (string, error) {
		resp, err := req.complete(ctx)
		return resp.Content.Text(), err
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
