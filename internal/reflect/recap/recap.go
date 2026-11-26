// Package recap generates a brief return-to-session summary.
package recap

import (
	"context"
	"strings"

	"github.com/genai-io/san/internal/llm"
)

// Instructions builds the request using facts already visible to the user.
func Instructions(facts, goal string) string {
	const lead = `<system-reminder>The user stepped away and is coming back. Recap in under 40 words, 1-2 plain sentences, no markdown, in the conversation's language. Lead with the overall goal and current task and where it stands, then the one next action, noting when it's theirs. Skip root-cause narrative, fix internals, secondary to-dos, and em-dash tangents.`
	prompt := lead
	if facts != "" {
		prompt += "\nThe user already sees these facts; don't repeat them: " + facts + "."
	}
	if goal != "" {
		prompt += "\nThe overall goal is AutoPilot's mission: " + goal
	}
	return prompt + "\n</system-reminder>"
}

// Generate obtains the recap from a prepared completion request.
func Generate(ctx context.Context, complete func(context.Context) (llm.CompletionResponse, error)) (string, error) {
	resp, err := complete(ctx)
	return strings.TrimSpace(resp.Content.Text()), err
}
