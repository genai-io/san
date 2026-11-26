package autopilot

// SuggestionPrompt predicts the user's next input when no mission is active.
// With a mission, it proposes an instruction toward that goal instead.
func SuggestionPrompt(steering, mission string) (prompt, details string) {
	if mission == "" {
		return `<system-reminder>Suggestion mode: predict what the user will type next in this session. You are a separate, one-off note: the main work is not interrupted, and you cannot use tools.
Predict what THEY would type, not what you think they should do: would they think "I was just about to type that"? Be specific: "run the tests" beats "continue".
Never suggest evaluations ("looks good"), questions, your own voice ("Let me…"), new ideas they didn't ask about, or more than one sentence.
Reply with nothing if the next step isn't obvious, or right after an error or a misunderstanding: let them assess it.
Reply with ONLY the suggestion, 2-12 words in the user's language and style: no quotes, no explanation.
</system-reminder>`, ""
	}
	return `<system-reminder>Suggestion mode: propose the SINGLE next instruction to give the agent toward the mission below. You are a separate, one-off note: the main work is not interrupted, and you cannot use tools.
Reply with ONLY that instruction, a short direct imperative the user can accept and send as-is (a few words to one sentence): no quotes, no explanation. Reply with nothing if the mission looks complete or the next step needs a human decision.
</system-reminder>`, "<steering_instructions>\n" + steering + "\n</steering_instructions>\n\nMission:\n" + mission
}
