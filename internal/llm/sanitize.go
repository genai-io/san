package llm

import "github.com/genai-io/san/internal/core"

// SanitizeToolMessages enforces tool-call/tool-result adjacency: an assistant
// message carrying tool calls must be followed immediately by the tool-result
// messages for those calls. It drops orphaned calls and results, which
// interrupted runs (mid-stream cancel) and restored sessions can leave behind.
//
// The rule is strict adjacency: for each assistant message, only the run of
// tool-result messages directly after it counts. A tool call with no matching
// following result is stripped; a tool result with no matching kept call is
// dropped. This is what the OpenAI Chat Completions / Responses APIs and the
// Gemini API both require, so the OpenAI-compatible and Google providers share
// this single implementation.
//
// Anthropic deliberately does NOT use this. Its client applies a broader
// variant (sanitizeToolResults) that matches results against any preceding
// tool_use in the chain rather than only the immediately adjacent assistant
// message; the two policies are not interchangeable, so keep them separate.
func SanitizeToolMessages(msgs []core.Message) []core.Message {
	result := make([]core.Message, 0, len(msgs))

	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]

		if msg.ToolResult != nil {
			// Tool results are only valid immediately after their assistant call.
			continue
		}

		if msg.Role != core.RoleAssistant || len(msg.ToolCalls) == 0 {
			result = append(result, msg)
			continue
		}

		j := i + 1
		var followingResults []core.Message
		for j < len(msgs) && msgs[j].ToolResult != nil {
			followingResults = append(followingResults, msgs[j])
			j++
		}

		resultIDs := make(map[string]bool, len(followingResults))
		for _, r := range followingResults {
			resultIDs[r.ToolResult.ToolCallID] = true
		}

		filteredCalls := make([]core.ToolCall, 0, len(msg.ToolCalls))
		callIDs := make(map[string]bool, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if resultIDs[tc.ID] {
				filteredCalls = append(filteredCalls, tc)
				callIDs[tc.ID] = true
			}
		}

		msg.ToolCalls = filteredCalls
		if len(msg.ToolCalls) > 0 || msg.Content != "" {
			result = append(result, msg)
		}

		seenResults := make(map[string]bool, len(followingResults))
		for _, r := range followingResults {
			id := r.ToolResult.ToolCallID
			if callIDs[id] && !seenResults[id] {
				result = append(result, r)
				seenResults[id] = true
			}
		}

		i = j - 1
	}

	return result
}
