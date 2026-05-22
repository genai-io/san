package conv

import (
	"strings"

	"github.com/genai-io/gen-code/internal/core"
)

// RenderContext bundles everything per-message rendering needs to read.
// One instance is built once per render pass (app.messageRenderParams)
// and threaded down through RenderActiveContent → RenderMessageRange
// → RenderMessageAt → renderAssistantWithTools.
type RenderContext struct {
	// ── Conversation state ──────────────────────────────────────
	Messages       []core.ChatMessage
	CommittedCount int
	// InlinedResults precomputes which ToolResult messages will be
	// drawn inline with their owning assistant (not as standalone
	// messages). Built once in one pass over Messages; lets every
	// render helper read the relationship without re-scanning.
	InlinedResults inlinedToolResults

	// ── Streaming + in-flight tool execution ────────────────────
	StreamActive bool
	BuildingTool string
	PendingCalls []core.ToolCall
	CurrentIdx   int

	// ── Renderer / terminal env ─────────────────────────────────
	Width      int
	MDRenderer *MDRenderer

	// ── Per-tick UI state ───────────────────────────────────────
	SpinnerView  string
	Blink        int
	ModelName    string
	InputTokens  int
	OutputTokens int

	// ── Decorations (color / progress maps) ─────────────────────
	AgentColors  map[string]string
	TaskProgress map[int][]string
	TaskOwnerMap map[string]string

	// ── Modal interlock — suppress chrome under a tool prompt ───
	InteractivePromptActive bool
}

// inlinedToolResults precomputes which ToolResult messages should be
// drawn inline with their owning assistant rather than as standalone
// messages, in one linear pass over the message slice. Three render
// helpers used to re-derive this independently with subtly different
// scans; centralising it here eliminates the drift risk.
type inlinedToolResults struct {
	// resultOwner: ToolResult message index → owning assistant index.
	// Used to know which results to SKIP when rendering a range (they
	// will be drawn inline with their owning assistant instead).
	resultOwner map[int]int
	// resultsForAssistant: assistant index → (toolCallID → ToolResultData)
	// Used by renderAssistantWithTools to draw the paired-results block.
	resultsForAssistant map[int]map[string]ToolResultData
}

// PrecomputeInlinedResults walks `messages` once and returns the
// inlining map. Exported because the model builds it inside
// messageRenderParams; everything else in this package reads
// RenderContext.InlinedResults.
func PrecomputeInlinedResults(messages []core.ChatMessage) inlinedToolResults {
	p := inlinedToolResults{
		resultOwner:         make(map[int]int),
		resultsForAssistant: make(map[int]map[string]ToolResultData),
	}
	for i, msg := range messages {
		if msg.Role != core.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		owned := make(map[string]struct{}, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			owned[tc.ID] = struct{}{}
		}
		for j := i + 1; j < len(messages); j++ {
			next := messages[j]
			if next.Role == core.RoleNotice {
				continue
			}
			if next.ToolResult == nil {
				break
			}
			callID := next.ToolResult.ToolCallID
			if _, ok := owned[callID]; !ok {
				continue
			}
			p.resultOwner[j] = i
			results := p.resultsForAssistant[i]
			if results == nil {
				results = make(map[string]ToolResultData)
				p.resultsForAssistant[i] = results
			}
			results[callID] = ToolResultData{
				ToolName: next.ToolName,
				Content:  next.ToolResult.Content,
				Error:    next.ToolResult.Content,
				IsError:  next.ToolResult.IsError,
				Expanded: next.Expanded,
			}
		}
	}
	return p
}

// ownerOf returns the index of the assistant message whose tool call
// produced the ToolResult at resultIdx, or -1 if the result doesn't
// belong to any assistant (orphan). Callers use this to know which
// ToolResult messages to skip during a range render — those are
// already being drawn by their owning assistant's block.
func (p inlinedToolResults) ownerOf(resultIdx int) int {
	if owner, ok := p.resultOwner[resultIdx]; ok {
		return owner
	}
	return -1
}

// resultsFor returns the toolCallID → ToolResultData map for an
// assistant's inlined results, or nil if none. renderAssistantWithTools
// uses this to assemble the result block that hangs underneath the
// assistant's text.
func (p inlinedToolResults) resultsFor(assistantIdx int) map[string]ToolResultData {
	return p.resultsForAssistant[assistantIdx]
}

// IsResultInlined reports whether the ToolResult at idx is going to be
// drawn inline with its owning assistant. RenderSingleMessage uses this
// to short-circuit standalone rendering for results that are already
// being shown via their assistant.
func (p inlinedToolResults) IsResultInlined(idx int) bool {
	_, ok := p.resultOwner[idx]
	return ok
}

func RenderMessageAt(p RenderContext, idx int, isStreaming bool) string {
	msg := p.Messages[idx]

	// The interrupt marker is a payload-only signal for the next LLM turn —
	// the user already sees "[Interrupted]" on the assistant message and
	// rendering this again as a `❯ ...` line reads as if they typed it.
	if msg.Role == core.RoleUser && msg.ToolResult == nil && msg.Content == InterruptedByUserMarker {
		return ""
	}

	var sb strings.Builder

	if msg.ToolResult == nil {
		sb.WriteString("\n")
	}

	switch msg.Role {
	case core.RoleUser:
		if msg.ToolResult != nil {
			sb.WriteString(RenderToolResultInline(ToolResultData{
				ToolName: msg.ToolName,
				Content:  msg.ToolResult.Content,
				Error:    msg.ToolResult.Content,
				IsError:  msg.ToolResult.IsError,
				Expanded: msg.Expanded,
			}, p.MDRenderer))
		} else {
			sb.WriteString(RenderUserMessage(msg.Content, msg.DisplayContent, msg.Images, p.MDRenderer, p.Width))
		}
	case core.RoleNotice:
		sb.WriteString(RenderSystemMessage(msg.Content))
	case core.RoleAssistant:
		sb.WriteString(renderAssistantWithTools(p, msg, idx, isStreaming))
	}

	return sb.String()
}

func renderAssistantWithTools(p RenderContext, msg core.ChatMessage, idx int, isLast bool) string {
	base := RenderAssistantMessage(AssistantParams{
		Content:       msg.Content,
		Thinking:      msg.Thinking,
		ToolCalls:     msg.ToolCalls,
		StreamActive:  p.StreamActive,
		IsLast:        isLast,
		SpinnerView:   p.SpinnerView,
		MDRenderer:    p.MDRenderer,
		Width:         p.Width,
		ExecutingTool: p.BuildingTool,
	})

	if len(msg.ToolCalls) == 0 {
		return base
	}

	var sb strings.Builder
	sb.WriteString(base)

	if msg.Content != "" {
		sb.WriteString("\n")
	}

	resultMap := p.InlinedResults.resultsFor(idx)
	if resultMap == nil {
		resultMap = map[string]ToolResultData{}
	}

	sb.WriteString(RenderToolCalls(ToolCallsParams{
		ToolCalls:         msg.ToolCalls,
		ToolCallsExpanded: msg.ToolCallsExpanded,
		ResultMap:         resultMap,
		ParallelMode:      len(p.PendingCalls) > 1,
		TaskProgress:      p.TaskProgress,
		PendingCalls:      p.PendingCalls,
		CurrentIdx:        p.CurrentIdx,
		ModelName:         p.ModelName,
		InputTokens:       p.InputTokens,
		OutputTokens:      p.OutputTokens,
		Blink:             p.Blink,
		AgentColors:       p.AgentColors,
		SpinnerView:       p.SpinnerView,
		TaskOwnerMap:      p.TaskOwnerMap,
		MDRenderer:        p.MDRenderer,
		Width:             p.Width,
	}))

	return sb.String()
}

func RenderMessageRange(p RenderContext, startIdx, endIdx int, includeSpinner bool) string {
	var sb strings.Builder

	lastIdx := endIdx - 1
	isLastStreaming := p.StreamActive && lastIdx >= 0 && p.Messages[lastIdx].Role == core.RoleAssistant

	for i := startIdx; i < endIdx; i++ {
		// Skip a ToolResult that will be drawn inline with its owning
		// assistant — but only if the owner is also being rendered in
		// this range. Orphan results render standalone.
		if owner := p.InlinedResults.ownerOf(i); owner >= startIdx {
			continue
		}
		isStreaming := i == lastIdx && isLastStreaming
		sb.WriteString(RenderMessageAt(p, i, isStreaming))
	}

	if includeSpinner {
		sb.WriteString(renderPendingToolSpinnerFromParams(p, startIdx < endIdx))
	}

	return sb.String()
}

func RenderSingleMessage(p RenderContext, idx int) string {
	if idx < 0 || idx >= len(p.Messages) {
		return ""
	}

	if p.Messages[idx].ToolResult != nil && p.InlinedResults.IsResultInlined(idx) {
		return ""
	}

	return strings.TrimRight(RenderMessageAt(p, idx, false), "\n")
}

func RenderActiveContent(p RenderContext) string {
	if p.CommittedCount >= len(p.Messages) {
		return renderPendingToolSpinnerFromParams(p, false)
	}
	return RenderMessageRange(p, p.CommittedCount, len(p.Messages), true)
}

func renderPendingToolSpinnerFromParams(p RenderContext, suppressAgentLabel bool) string {
	return RenderPendingToolSpinner(PendingToolSpinnerParams{
		InteractivePromptActive: p.InteractivePromptActive,
		BuildingTool:            p.BuildingTool,
		PendingCalls:            p.PendingCalls,
		CurrentIdx:              p.CurrentIdx,
		TaskProgress:            p.TaskProgress,
		SpinnerView:             p.SpinnerView,
		Blink:                   p.Blink,
		AgentColors:             p.AgentColors,
		Width:                   p.Width,
		SuppressAgentLabel:      suppressAgentLabel,
	})
}
