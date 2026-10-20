package conv

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
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
	StreamActive    bool
	StreamWaiting   bool
	StreamMessageID string
	BuildingTool    string
	PendingCalls    []core.ToolCall
	CurrentIdx      int
	// ToolStartedAt maps a running tool call's ID to when it began executing,
	// so its row can show a live elapsed timer. Only in-flight calls appear.
	ToolStartedAt map[string]time.Time
	// ToolProgress maps a running command's ID to its latest output counter
	// ("1.2k lines"), shown next to the timer. Empty for calls with no output.
	ToolProgress map[string]string
	// AwaitingApprovalID is the in-flight call parked on a permission prompt.
	// It was stamped as started before the prompt went out, so its row has to
	// be told apart from the siblings that really are executing.
	AwaitingApprovalID string

	// ── Renderer / terminal env ─────────────────────────────────
	Width      int
	MDRenderer *MDRenderer
	// ThinkingDisplay is the resolved reasoning-display mode (one of the
	// setting.ThinkingDisplay* constants), threaded down to the assistant
	// renderer so it knows whether to draw the reasoning body, a
	// "Thought for 3.2 s" line, or nothing.
	ThinkingDisplay string

	// ── Per-tick UI state ───────────────────────────────────────
	SpinnerView  string
	Blink        int
	ModelName    string
	InputTokens  int
	OutputTokens int

	// ── Decorations (color / activity maps) ─────────────────────
	AgentColors  map[string]string
	MemberColors map[string]int // group member → palette slot, fixed for the session
	TaskActivity map[int][]string
	TaskOwnerMap map[string]string

	// ── Modal interlock — suppress chrome under a tool prompt ───
	// DockedModalActive marks the frames where a Question / Approval / secret
	// modal owns the screen. The chat tail still renders above it, so anything
	// that animates has to hold still: the turn is parked on the user's answer,
	// not making progress.
	DockedModalActive bool

	// Interactive marks the live render pass (RenderActiveContent): the
	// uncommitted tail the user can still act on. "(ctrl+o to expand)" hints
	// show only here — once a message is frozen into native scrollback the
	// key no longer reaches it.
	Interactive bool
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

// PrecomputeInlinedResults walks `messages[from:]` once and returns the
// inlining map. `from` is the caller's CommittedCount: the render only ever
// draws [from:] (committed messages live in native scrollback), so scanning the
// whole conversation every frame is pure waste that grows with session length.
// Absolute indices are preserved — the maps key by position in the full slice,
// and a result at index ≥ from is always owned by an assistant at index ≥ from
// (an assistant commits together with its tool results), so nothing is missed.
// Exported because the model builds it inside messageRenderParams; everything
// else in this package reads RenderContext.InlinedResults.
func PrecomputeInlinedResults(messages []core.ChatMessage, from int) inlinedToolResults {
	p := inlinedToolResults{
		resultOwner:         make(map[int]int),
		resultsForAssistant: make(map[int]map[string]ToolResultData),
	}
	if from < 0 {
		from = 0
	}
	for i := from; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != core.ChatAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		owned := make(map[string]struct{}, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			owned[tc.ID] = struct{}{}
		}
		for j := i + 1; j < len(messages); j++ {
			next := messages[j]
			if next.Role == core.ChatNotice || (next.Role == core.ChatUser && next.ToolResult == nil && IsAgentEnvelope(next.Content)) {
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
				ToolName: next.ToolResult.ToolName,
				Content:  next.ToolResult.Content.Text(),
				IsError:  next.ToolResult.IsError,
				Expanded: next.Expanded,
				Decision: next.Decision,
				Details:  next.ToolDetails,
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
	var sb strings.Builder

	// A copilot-produced turn (continuation or rewrite) wears a "⎿ autopilot ·
	// <note>" annotation hugging its "❭" line from below (no blank between) so
	// the trailer corner hangs it under the instruction; the normal blank line
	// above still separates it from the turn before.
	autopilotDriven := msg.Role == core.ChatUser && msg.ToolResult == nil && msg.AutopilotNote != ""
	if p.memoryStep(msg, idx) {
		return p.memoryStepView(msg, idx)
	}

	if msg.ToolResult == nil {
		sb.WriteString("\n")
	}

	switch msg.Role {
	case core.ChatUser:
		switch {
		case msg.ToolResult != nil:
			sb.WriteString(RenderToolResultInline(ToolResultData{
				ToolName:    msg.ToolResult.ToolName,
				Content:     msg.ToolResult.Content.Text(),
				IsError:     msg.ToolResult.IsError,
				Expanded:    msg.Expanded,
				Interactive: p.Interactive,
				Width:       p.Width,
				Details:     msg.ToolDetails,
			}, p.MDRenderer))
		case core.IsCompactSummary(msg.Content):
			// The post-compaction summary is injected as a user message (the
			// model reads it, and it persists/seeds resume), but it's shown as a
			// system notice — not a "❭" user turn — so the transcript stays clean.
			sb.WriteString(RenderSystemMessage(msg.DisplayContent))
		case IsAgentEnvelope(msg.Content):
			// A background-agent envelope (task completion / relayed message) is
			// submitted to the model as a user turn and persisted as raw XML.
			// Live and resume share this summary; history can expose the body.
			sb.WriteString(RenderAgentNotice(agentEnvelopeSummary(msg.Content, p.Width), p.Width, p.MemberColors))
			if msg.Expanded {
				tag, body, _ := strings.Cut(msg.Content, ">")
				if end := strings.LastIndex(body, "</"); end >= 0 {
					body = body[:end]
				}
				body = html.UnescapeString(strings.TrimSpace(body))
				if file := envelopeAttr(tag, "output-file"); file != "" {
					body += "\n\nOutput file: " + file
				}
				sb.WriteString(renderNestedToolBody(body, p.Width))
			}
		default:
			sb.WriteString(RenderUserMessage(msg.Content, msg.DisplayContent, msg.Images, p.MDRenderer, p.Width))
			if autopilotDriven {
				sb.WriteString(RenderAutopilotMark(msg.AutopilotNote))
			}
		}
	case core.ChatNotice:
		if msg.AgentNotice {
			sb.WriteString(RenderAgentNotice(msg.Content, p.Width, p.MemberColors))
		} else {
			sb.WriteString(RenderSystemMessage(msg.Content))
		}
	case core.ChatAssistant:
		sb.WriteString(renderAssistantWithTools(p, msg, idx, isStreaming))
	}

	return sb.String()
}

// executingLabel is what the running tool is doing, in place of "Thinking...".
func (p RenderContext) executingLabel() string {
	if p.BuildingTool == "" || p.CurrentIdx >= len(p.PendingCalls) {
		return ""
	}
	return executionLabel(p.PendingCalls[p.CurrentIdx])
}

func renderAssistantWithTools(p RenderContext, msg core.ChatMessage, idx int, isLast bool) string {
	base := RenderAssistantMessage(AssistantParams{
		Content:              msg.Content,
		Thinking:             msg.Thinking,
		ToolCalls:            msg.ToolCalls,
		StreamActive:         p.StreamActive,
		IsLast:               isLast,
		DockedModalActive:    p.DockedModalActive,
		SpinnerView:          p.SpinnerView,
		MDRenderer:           p.MDRenderer,
		Width:                p.Width,
		ExecutingLabel:       p.executingLabel(),
		ContentCommittedLen:  msg.ContentCommittedLen,
		ThinkingCommittedLen: msg.ThinkingCommittedLen,
		BulletEmitted:        msg.BulletEmitted,
		ThinkingEmitted:      msg.ThinkingEmitted,
		ThinkingDisplay:      p.ThinkingDisplay,
		ThinkingDuration:     msg.ThinkingDuration,
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
		ToolCalls:          msg.ToolCalls,
		ToolCallsExpanded:  msg.ToolCallsExpanded,
		ResultMap:          resultMap,
		ParallelMode:       len(p.PendingCalls) > 1,
		DockedModalActive:  p.DockedModalActive,
		TaskActivity:       p.TaskActivity,
		PendingCalls:       p.PendingCalls,
		CurrentIdx:         p.CurrentIdx,
		ToolStartedAt:      p.ToolStartedAt,
		ToolProgress:       p.ToolProgress,
		AwaitingApprovalID: p.AwaitingApprovalID,
		ModelName:          p.ModelName,
		InputTokens:        p.InputTokens,
		OutputTokens:       p.OutputTokens,
		Blink:              p.Blink,
		AgentColors:        p.AgentColors,
		MemberColors:       p.MemberColors,
		SpinnerView:        p.SpinnerView,
		TaskOwnerMap:       p.TaskOwnerMap,
		MDRenderer:         p.MDRenderer,
		Width:              p.Width,
		Interactive:        p.Interactive,
	}))

	return sb.String()
}

func RenderMessageRange(p RenderContext, startIdx, endIdx int, includeSpinner bool) string {
	var sb strings.Builder

	for i := startIdx; i < endIdx; i++ {
		// Skip a ToolResult that will be drawn inline with its owning
		// assistant — but only if the owner is also being rendered in
		// this range. Orphan results render standalone.
		if owner := p.InlinedResults.ownerOf(i); owner >= startIdx {
			continue
		}
		isStreaming := p.StreamActive && p.StreamMessageID != "" && p.Messages[i].ID == p.StreamMessageID
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

type activeWorkerCall struct {
	call     core.ToolCall
	expanded bool
}

// activeWorkerCalls includes ordinary pending siblings when a worker layout is
// needed. Replacing the tail must not silently remove a running shell or fetch.
func activeWorkerCalls(p RenderContext) []activeWorkerCall {
	pending := make(map[string]bool, len(p.PendingCalls))
	for _, call := range p.PendingCalls {
		pending[call.ID] = true
	}
	var calls []activeWorkerCall
	hasWorker := false
	for i := p.CommittedCount; i < len(p.Messages); i++ {
		msg := p.Messages[i]
		results := p.InlinedResults.resultsFor(i)
		for _, call := range msg.ToolCalls {
			if _, done := results[call.ID]; done || !pending[call.ID] {
				continue
			}
			delete(pending, call.ID)
			calls = append(calls, activeWorkerCall{call: call, expanded: msg.ToolCallsExpanded})
			hasWorker = hasWorker || (tool.IsAgentToolName(call.Name) && call.Name != tool.ToolSendMessage)
		}
	}
	if !hasWorker {
		return nil
	}
	return calls
}

func latestActiveNoticeIndex(p RenderContext) int {
	for i := len(p.Messages) - 1; i >= p.CommittedCount; i-- {
		if p.Messages[i].Role == core.ChatNotice {
			return i
		}
	}
	return -1
}

// ActiveWorkerRows reserves one identity per pending tool, plus the latest
// notice, before background indicators spend the remaining terminal height.
// It returns zero when no foreground worker layout is needed.
func ActiveWorkerRows(p RenderContext) int {
	rows := len(activeWorkerCalls(p))
	if rows > 0 && latestActiveNoticeIndex(p) >= 0 {
		rows++
	}
	return rows
}

// RenderActiveWorkers reserves identities before spending rows on recent details.
// Ordinary pending tool siblings share the same budget rather than disappearing.
// The bool reports whether the worker layout owns the active tail this frame.
func RenderActiveWorkers(p RenderContext, height int) (string, bool) {
	var headers, details []string
	omittedLabel := "active workers omitted"
	for _, active := range activeWorkerCalls(p) {
		call := active.call
		expanded := active.expanded
		if !tool.IsAgentToolName(call.Name) || call.Name == tool.ToolSendMessage {
			omittedLabel = "active tools omitted"
			// Ordinary siblings need their compact command/description on the
			// identity row, not an expanded body that may receive no detail rows.
			expanded = false
		}
		rendered := RenderToolCalls(ToolCallsParams{
			ToolCalls: []core.ToolCall{call}, ToolCallsExpanded: expanded,
			PendingCalls: p.PendingCalls, TaskActivity: p.TaskActivity,
			ParallelMode: len(p.PendingCalls) > 1, Width: p.Width,
			Blink: p.Blink, AgentColors: p.AgentColors,
			DockedModalActive: p.DockedModalActive, AwaitingApprovalID: p.AwaitingApprovalID,
		})
		lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
		headers = append(headers, lines[0])
		details = append(details, strings.Join(lines[1:], "\n"))
	}
	if len(headers) == 0 {
		return "", false
	}
	if height <= 0 {
		return "", true
	}
	// Keep the newest notice visible while workers run. Reserving identities
	// must not hide the error delivery fixed in #613.
	notice := ""
	if i := latestActiveNoticeIndex(p); i >= 0 {
		notice = strings.TrimSpace(RenderMessageAt(p, i, false))
		notice = strings.Join(strings.Fields(notice), " ")
	}
	workerHeight := height
	if notice != "" {
		workerHeight--
	}
	if len(headers) > workerHeight {
		rows := []string{}
		if workerHeight > 0 {
			visible := max(0, workerHeight-1)
			rows = append(rows, headers[:visible]...)
			rows = append(rows, fmt.Sprintf("  … %d %s", len(headers)-visible, omittedLabel))
		}
		if notice != "" {
			rows = append(rows, notice)
		}
		return boundedWorkerRows(rows, p.Width), true
	}
	budget := workerHeight - len(headers)
	var detailLines [][]string
	total := 0
	for _, detail := range details {
		var lines []string
		if detail != "" {
			lines = strings.Split(detail, "\n")
		}
		detailLines = append(detailLines, lines)
		total += len(lines)
	}
	showOmissions := total > budget && budget > 0
	if showOmissions {
		budget--
	}
	allocated := make([]int, len(headers))
	for budget > 0 {
		advanced := false
		for i, lines := range detailLines {
			if allocated[i] < len(lines) && budget > 0 {
				allocated[i]++
				budget--
				advanced = true
			}
		}
		if !advanced {
			break
		}
	}
	var rows []string
	shown := 0
	for i, header := range headers {
		rows = append(rows, header)
		n := allocated[i]
		lines := detailLines[i]
		// Keep the newest action, not an arbitrary slice of the briefing.
		rows = append(rows, lines[len(lines)-n:]...)
		shown += n
	}
	if showOmissions {
		rows = append(rows, fmt.Sprintf("  … %d detail rows omitted", total-shown))
	}
	if notice != "" {
		rows = append(rows, notice)
	}
	return boundedWorkerRows(rows, p.Width), true
}

func boundedWorkerRows(rows []string, width int) string {
	for i, row := range rows {
		rows[i] = kit.TruncateText(row, max(1, width))
	}
	return strings.Join(rows, "\n")
}

func RenderActiveContent(p RenderContext) string {
	p.Interactive = true
	content := RenderMessageRange(p, p.CommittedCount, len(p.Messages), true)
	// A turn between steps (one committed, the next not yet streaming) keeps
	// its spinner, or the row would blink out and back.
	if p.StreamWaiting || p.StreamActive && strings.TrimSpace(content) == "" {
		content += "\n" + RenderAssistantMessage(AssistantParams{
			StreamActive: true, IsLast: true, SpinnerView: p.SpinnerView,
			DockedModalActive: p.DockedModalActive, Width: p.Width,
		})
	}
	return content
}

func renderPendingToolSpinnerFromParams(p RenderContext, suppressAgentLabel bool) string {
	return RenderPendingToolSpinner(PendingToolSpinnerParams{
		DockedModalActive:  p.DockedModalActive,
		BuildingTool:       p.BuildingTool,
		PendingCalls:       p.PendingCalls,
		CurrentIdx:         p.CurrentIdx,
		TaskActivity:       p.TaskActivity,
		SpinnerView:        p.SpinnerView,
		Blink:              p.Blink,
		AgentColors:        p.AgentColors,
		Width:              p.Width,
		SuppressAgentLabel: suppressAgentLabel,
	})
}
