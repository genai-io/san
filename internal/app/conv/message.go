// Pure message rendering functions that take explicit parameters instead of model state.
package conv

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

const (
	// minWrapWidth is the minimum markdown wrap width.
	minWrapWidth = 40

	// streamWrapReserve is the column budget held back when plain-wrapping the
	// live streaming tail: 2 cols for the "● "/"✦ " gutter plus 2 cols of slack
	// so an ambiguous-width glyph (the spinner star ✶ counted 1 cell but drawn
	// 2, CJK, box-drawing) can't push a line into the last column — that makes
	// the inline-mode insertAbove miscount rows and weld a stale frame into
	// scrollback.
	streamWrapReserve = 4

	// agentContentIndent is the extra indent for agent prompt/response content
	// beyond toolResultExpandedStyle's PaddingLeft(4). Total indent = 4 + 4 = 8 chars.
	agentContentIndent = "    "
)

// OperationModeParams holds the parameters needed for rendering mode status.
type OperationModeParams struct {
	Mode             setting.OperationMode
	InputTokens      int
	InputLimit       int
	ModelName        string
	StatusMessage    string
	ConversationCost llm.Money
	Compressions     int  // session compact count, drives the "compacted ×N" badge
	ShowContextBar   bool // render the visual [██████░░░░] 71% bar (opt-in)
	Width            int
	ThinkingEffort   string
	ShowThinking     bool
	QueueCount       int
}

// RenderModeStatus renders the combined mode status line.
func RenderModeStatus(params OperationModeParams) string {
	var leftParts []string

	if modeStatus := RenderOperationModeIndicator(params.Mode); modeStatus != "" {
		leftParts = append(leftParts, modeStatus)
	}

	if params.ShowThinking {
		if thinkingStatus := RenderThinkingIndicator(params.ThinkingEffort); thinkingStatus != "" {
			leftParts = append(leftParts, thinkingStatus)
		}
	}

	if queueBadge := renderQueueBadge(params.QueueCount); queueBadge != "" {
		leftParts = append(leftParts, queueBadge)
	}

	left := strings.Join(leftParts, "  ")

	right := renderStatusCluster(params)
	if right == "" || params.Width <= 0 {
		return left
	}

	gap := max(2, params.Width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	return left + strings.Repeat(" ", gap) + right
}

// renderStatusCluster composes the status line's right-hand cluster, in
// display order: model name, optional transient status message, the numeric
// "ctx X/Y" label, the optional visual context bar, the optional compressions
// badge, and the optional cost. Each piece is a statusSegment with a drop
// priority; fitStatusSegments drops the least important first when the
// terminal is too narrow to hold them all.
func renderStatusCluster(p OperationModeParams) string {
	if p.ModelName == "" {
		return ""
	}
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	sep := muted.Render(" · ")

	// Priority 1 = most important (dropped last). The model name always
	// renders; everything else drops before it under width pressure.
	segments := []statusSegment{
		{text: muted.Render(p.ModelName), priority: 1},
	}
	if p.StatusMessage != "" {
		segments = append(segments, statusSegment{text: muted.Render(p.StatusMessage), priority: 2})
	}

	// The numeric label always renders — it falls back to "ctx X/--" when the
	// limit is unknown, so the slot stays visible instead of silently hiding.
	segments = append(segments, statusSegment{
		text:     RenderContextLabel(p.InputTokens, p.InputLimit),
		priority: 3,
	})

	// The visual bar is opt-in (off by default). When shown it also carries
	// the auto-compact hint as a near-full warning.
	if p.ShowContextBar {
		bar := RenderContextBar(p.InputTokens, p.InputLimit)
		if p.InputLimit > 0 {
			if hint := compactStatusHint(float64(p.InputTokens) / float64(p.InputLimit) * 100); hint != "" {
				bar += sep + muted.Render(hint)
			}
		}
		segments = append(segments, statusSegment{text: bar, priority: 4})
	}

	if badge := RenderCompressionsBadge(p.Compressions); badge != "" {
		segments = append(segments, statusSegment{text: badge, priority: 5})
	}
	if !p.ConversationCost.IsZero() {
		segments = append(segments, statusSegment{text: muted.Render(kit.FormatMoney(p.ConversationCost)), priority: 6})
	}

	survivors := fitStatusSegments(segments, p.Width, lipgloss.Width(sep))
	return strings.Join(survivors, sep)
}

func RenderTurnUsageSummary(inputTokens, outputTokens, width int) string {
	if inputTokens == 0 && outputTokens == 0 {
		return ""
	}

	summary := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(
		fmt.Sprintf("↑%s ↓%s", kit.FormatTokenCount(inputTokens), kit.FormatTokenCount(outputTokens)),
	)
	if width <= 0 {
		return summary
	}

	gap := max(0, width-lipgloss.Width(summary))
	return strings.Repeat(" ", gap) + summary
}

func compactStatusHint(percent float64) string {
	switch {
	case percent >= pctCritical:
		return "auto-compact"
	case percent > pctWarn:
		return fmt.Sprintf("compact at %d%%", int(pctCritical))
	default:
		return ""
	}
}

// RenderOperationModeIndicator returns the mode status indicator for auto-accept or bypass mode.
func RenderOperationModeIndicator(mode setting.OperationMode) string {
	var icon, label string
	var clr kit.AdaptiveColor

	switch mode {
	case setting.ModeAutoAccept:
		icon = "⏵⏵"
		label = " accept edits on"
		clr = kit.CurrentTheme.Success
	case setting.ModeBypassPermissions:
		icon = "⏵⏵"
		label = " bypass permissions on"
		clr = kit.CurrentTheme.Error
	default:
		return ""
	}

	style := lipgloss.NewStyle().Foreground(clr)
	hint := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(" (shift+tab to cycle)")
	return "  " + style.Render(icon+label) + hint
}

func RenderThinkingIndicator(effort string) string {
	if effort == "" || effort == "off" || effort == "none" {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	hint := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(" (ctrl+t to cycle)")
	return "  " + style.Render("✦ "+effort) + hint
}

// toolResultIcon returns the icon for tool results based on error state.
func toolResultIcon(isError bool) string {
	if isError {
		return "✗"
	}
	return "⎿"
}

var (
	userMsgStyle = lipgloss.NewStyle()

	InputPromptStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Focus).
				Bold(true)

	// The assistant bullet leads with weight, not hue: the strongest neutral
	// (near-black on light, near-white on dark) plus bold. The conversation
	// body stays monochrome — teal is reserved for the user's "❭" marker.
	aiPromptStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.TextBright).
			Bold(true)

	// Footer rules are a faint hairline so they frame the input without
	// drawing ink — softer than the bluish Separator used between messages.
	SeparatorStyle = lipgloss.NewStyle().
			Foreground(kit.AdaptiveColor{Dark: "#3F3F46", Light: "#E4E4E7"})

	ThinkingStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted)

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.TextDim).
			PaddingLeft(2)

	// The tool call line stays readable — the action and its target (the file
	// being edited, the command being run) matter, and the live spinner rides
	// this line while the call is in flight.
	toolCallStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Text)

	// Only the "⎿ … → size" result trailer recedes: it's secondary metadata,
	// dimmed to the same supporting tone as the expanded body so the eye lands
	// on the assistant's prose and the actions, not the bookkeeping.
	toolResultStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.TextDim)

	toolResultExpandedStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				PaddingLeft(4)

	agentLabelStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Success)

	trackerPendingStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Muted)

	trackerInProgressStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Primary).
				Bold(true)

	trackerCompletedStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Success)

	PendingImageStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Primary)

	SelectedImageStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextBright).
				Background(kit.CurrentTheme.Primary).
				Bold(true)
)
var inlineImageTokenPattern = regexp.MustCompile(`\[Image #\d+\]`)

// RenderUserMessage renders a user message with prompt and optional images.
func RenderUserMessage(content, displayContent string, images []core.Image, mdRenderer *MDRenderer, width int) string {
	var sb strings.Builder
	prompt := InputPromptStyle.Render("❭ ")
	if displayContent == "" {
		displayContent = content
	}

	if len(images) > 0 && inlineImageTokenPattern.MatchString(displayContent) {
		sb.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top,
			prompt,
			userMsgStyle.Render(styleInlineImageTokens(displayContent)),
		) + "\n")
		return sb.String()
	}

	if len(images) > 0 {
		imgParts := make([]string, 0, len(images))
		for i := range images {
			imgParts = append(imgParts, PendingImageStyle.Render(fmt.Sprintf("[Image #%d]", i+1)))
		}
		imageLabel := strings.Join(imgParts, " ")
		if displayContent != "" {
			sb.WriteString(prompt + imageLabel + " " + userMsgStyle.Render(displayContent) + "\n")
		} else {
			sb.WriteString(prompt + imageLabel + "\n")
		}
	} else if displayContent != "" {
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, prompt, userMsgStyle.Render(displayContent)) + "\n")
	}

	return sb.String()
}

func styleInlineImageTokens(content string) string {
	return inlineImageTokenPattern.ReplaceAllStringFunc(content, func(token string) string {
		return PendingImageStyle.Render(token)
	})
}

// AssistantParams holds the parameters for rendering an assistant core.
type AssistantParams struct {
	Content           string
	Thinking          string
	ToolCalls         []core.ToolCall
	ToolCallsExpanded bool
	StreamActive      bool
	IsLast            bool
	SpinnerView       string
	MDRenderer        *MDRenderer
	Width             int
	ExecutingTool     string

	// Streaming-commit offsets: how much of Content/Thinking is already in
	// scrollback (see FlushStreamingBlocks). Only the remainder is rendered
	// here. BulletEmitted swaps the "● " marker for a continuation gutter once
	// the turn's first content block has been committed.
	ContentCommittedLen  int
	ThinkingCommittedLen int
	BulletEmitted        bool
}

// InterruptedMarker is the literal suffix MarkLastInterrupted appends to an
// assistant message's Content when the user cancels mid-stream. It lives on
// the conv-side ChatMessage only — handleStreamCancel no longer pushes conv
// state back into the agent, so the marker reaches the LLM only via session
// save+reload. Stripped at render time so the UI shows a styled badge
// instead of inline text.
const InterruptedMarker = "[Interrupted]"

// continuationGutter is the 2-column blank that aligns continuation lines, and
// content blocks committed after the first, under the "● " assistant marker.
const continuationGutter = "  "

// contentGutter returns the 2-column lead for an assistant content block: the
// "● " marker for the turn's first content, or a blank gutter for blocks that
// continue a turn whose marker was already emitted.
func contentGutter(showBullet bool) string {
	if showBullet {
		return aiPromptStyle.Render("● ")
	}
	return continuationGutter
}

// renderThinkingBlock renders reasoning text as the muted "✦" block shared by
// the live view and the scrollback commit path. The glyph and text both stay
// muted, matching the status-bar thinking indicator — no hue.
func renderThinkingBlock(thinking string, width int) string {
	wrapWidth := max(width-streamWrapReserve, minWrapWidth)
	wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(thinking)
	var lines []string
	for line := range strings.SplitSeq(wrapped, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, ThinkingStyle.Render(line))
		}
	}
	thinkingIcon := ThinkingStyle.Render("✦ ")
	return lipgloss.JoinHorizontal(lipgloss.Top, thinkingIcon, strings.Join(lines, "\n"))
}

// sliceFrom returns the portion of s past the first n bytes — the not-yet-
// committed remainder of a streaming field. n out of range collapses to the
// natural answer (whole string for n<=0, empty once n has caught up to len).
func sliceFrom(s string, n int) string {
	switch {
	case n <= 0:
		return s
	case n >= len(s):
		return ""
	default:
		return s[n:]
	}
}

// RenderCommittedThinkingBlock renders a completed reasoning block for commit to
// native scrollback — the muted "✦" gutter plus the wrapped thinking text.
func RenderCommittedThinkingBlock(thinking string, width int) string {
	if strings.TrimSpace(thinking) == "" {
		return ""
	}
	return renderThinkingBlock(thinking, width)
}

// RenderCommittedContentBlock renders one or more completed markdown blocks of
// assistant content for commit to native scrollback. showBullet leads the block
// with the "● " marker (the turn's first content) or a blank continuation
// gutter. Returns "" when the slice renders empty (e.g. only blank lines).
func RenderCommittedContentBlock(content string, showBullet bool, md *MDRenderer) string {
	body := content
	if md != nil {
		body = renderMarkdownContent(md, content)
	}
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, contentGutter(showBullet), body)
}

// RenderAssistantMessage renders an assistant message with thinking, content, and tool calls.
func RenderAssistantMessage(params AssistantParams) string {
	var sb strings.Builder

	// Render only the not-yet-committed remainder: completed blocks of a
	// streaming message are already in scrollback (see FlushStreamingBlocks).
	params.Thinking = sliceFrom(params.Thinking, params.ThinkingCommittedLen)
	params.Content = sliceFrom(params.Content, params.ContentCommittedLen)

	// The first content of a turn leads with "● "; once a block has been
	// committed the bullet is spent and the remainder aligns under a gutter.
	// While streaming, the active tail shows the spinner in the bullet slot.
	aiIcon := contentGutter(!params.BulletEmitted)
	if params.StreamActive && params.IsLast {
		aiIcon = aiPromptStyle.Render(params.SpinnerView + " ")
	}

	interrupted := false
	switch {
	case strings.HasSuffix(params.Content, " "+InterruptedMarker):
		params.Content = strings.TrimSuffix(params.Content, " "+InterruptedMarker)
		interrupted = true
	case params.Content == InterruptedMarker:
		params.Content = ""
		interrupted = true
	}

	if params.Thinking != "" {
		sb.WriteString(renderThinkingBlock(params.Thinking, params.Width) + "\n\n")
	}

	content := formatAssistantContent(params)
	if content != "" {
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, aiIcon, content) + "\n")
	}

	if interrupted {
		sb.WriteString("  " + ThinkingStyle.Render("⏸ interrupted by user") + "\n")
	}

	return sb.String()
}

// formatAssistantContent formats the assistant message content based on streaming state.
func formatAssistantContent(params AssistantParams) string {
	if params.Content == "" && len(params.ToolCalls) == 0 && params.StreamActive && params.Thinking == "" {
		if params.ExecutingTool != "" {
			return ThinkingStyle.Render(getToolExecutionDesc(params.ExecutingTool))
		}
		if !params.BulletEmitted {
			return ThinkingStyle.Render("Thinking...")
		}
		// BulletEmitted: content is already streaming to scrollback block by
		// block — fall through to the bare streaming cursor so the gap between
		// committed blocks still shows live activity, not the "Thinking…" filler.
	}

	if params.StreamActive && params.IsLast && len(params.ToolCalls) == 0 {
		// Plain-wrap the streaming tail so its \n-line count matches the height
		// calc; reserve streamWrapReserve cols (gutter + last-column slack).
		wrapWidth := max(params.Width-streamWrapReserve, minWrapWidth)
		return lipgloss.NewStyle().Width(wrapWidth).Render(params.Content + "▌")
	}

	if params.Content == "" {
		return ""
	}

	if params.MDRenderer != nil {
		return renderMarkdownContent(params.MDRenderer, params.Content)
	}

	return params.Content
}

// renderMarkdownContent renders content through the markdown renderer, dropping
// glamour's full-width blank margin lines so blocks don't accrue extra vertical
// gaps (especially when committed to scrollback block-by-block while streaming).
func renderMarkdownContent(mdRenderer *MDRenderer, content string) string {
	rendered, err := mdRenderer.Render(content)
	if err != nil {
		return content
	}
	return trimStyledBlankLines(rendered)
}

// getToolExecutionDesc returns a human-readable description for a tool being executed.
func getToolExecutionDesc(toolName string) string {
	switch toolName {
	case "Read":
		return "Reading file..."
	case "Write":
		return "Writing file..."
	case "Edit":
		return "Editing file..."
	case "Bash":
		return "Executing command..."
	case "Glob":
		return "Finding files..."
	case "Grep":
		return "Searching files..."
	case "WebFetch":
		return "Fetching web content..."
	case "WebSearch":
		return "Searching the web..."
	case "AskUserQuestion":
		return "Preparing question..."
	case tool.ToolSkill:
		return "Loading skill..."
	default:
		return "Executing..."
	}
}

// RenderSystemMessage renders a system/notice core.
func RenderSystemMessage(content string) string {
	return systemMsgStyle.Render(content) + "\n"
}

// ToolCallsParams holds the parameters for rendering tool calls.
type ToolCallsParams struct {
	ToolCalls         []core.ToolCall
	ToolCallsExpanded bool
	ResultMap         map[string]ToolResultData
	ParallelMode      bool
	TaskProgress      map[int][]string
	PendingCalls      []core.ToolCall
	CurrentIdx        int
	ModelName         string
	InputTokens       int
	OutputTokens      int
	Blink             int
	AgentColors       map[string]string
	SpinnerView       string
	TaskOwnerMap      map[string]string
	MDRenderer        *MDRenderer
	Width             int
}

// ToolResultData holds the data needed to render a tool result inline.
type ToolResultData struct {
	ToolName  string
	Content   string
	Error     string
	IsError   bool
	Expanded  bool
	ToolInput string
}

// RenderToolCalls renders the tool calls section of an assistant core.
func RenderToolCalls(params ToolCallsParams) string {
	var sb strings.Builder

	for _, tc := range params.ToolCalls {
		switch tc.Name {
		case tool.ToolTaskList, tool.ToolTaskCreate, tool.ToolTaskUpdate:
			continue
		}
		if tool.IsAgentToolName(tc.Name) {
			label := formatAgentLabel(tc.Input)
			color := agentColorForInput(tc.Input, params.AgentColors)
			_, hasResult := params.ResultMap[tc.ID]
			if hasResult {
				sb.WriteString(renderAgentToolLine(label, params.Width, "●", color) + "\n")
			} else {
				sb.WriteString(renderAgentToolLine(label, params.Width, agentIcon(params.Blink), color))
				if !params.ToolCallsExpanded {
					sb.WriteString(ThinkingStyle.Render("  (ctrl+o to expand)"))
				}
				sb.WriteString("\n")
			}
			if params.ToolCallsExpanded && !hasResult {
				sb.WriteString(formatAgentDefinition(tc.Input, params.Width))
			}
		} else if params.ToolCallsExpanded {
			toolLine := renderToolLine(tc.Name, params.Width)
			sb.WriteString(toolLine + "\n")
			var p map[string]any
			if err := json.Unmarshal([]byte(tc.Input), &p); err == nil {
				for k, v := range p {
					if s, ok := v.(string); ok {
						if len(s) > 80 {
							sb.WriteString(toolResultExpandedStyle.Render(fmt.Sprintf("%s:", k)) + "\n")
							sb.WriteString(toolResultExpandedStyle.Render(s) + "\n")
						} else {
							sb.WriteString(toolResultExpandedStyle.Render(fmt.Sprintf("%s: %s", k, s)) + "\n")
						}
					}
				}
			}
		} else {
			icon := toolCallIcon(tc, params.PendingCalls, params.CurrentIdx, params.ParallelMode, params.SpinnerView)
			if _, hasResult := params.ResultMap[tc.ID]; hasResult {
				icon = "●"
			}
			if tc.Name == tool.ToolTaskGet && params.TaskOwnerMap != nil {
				args := extractTaskGetDisplay(tc.Input, params.TaskOwnerMap)
				sb.WriteString(renderToolLineWithIcon(fmt.Sprintf("%s(%s)", tc.Name, args), params.Width, icon) + "\n")
			} else {
				args := extractToolArgs(tc.Input)
				sb.WriteString(renderToolLineWithIcon(fmt.Sprintf("%s(%s)", tc.Name, args), params.Width, icon) + "\n")
			}
		}

		if resultData, ok := params.ResultMap[tc.ID]; ok {
			resultData.ToolInput = tc.Input
			sb.WriteString(RenderToolResultInline(resultData, params.MDRenderer))
		} else if tool.IsAgentToolName(tc.Name) {
			limit := maxCompactAgentToolLines
			if params.ParallelMode {
				limit = maxParallelAgentToolLines
			}
			sb.WriteString(renderAgentProgressInline(tc, params.PendingCalls, params.TaskProgress, params.ToolCallsExpanded, limit, AgentStats{
				Model:        params.ModelName,
				InputTokens:  params.InputTokens,
				OutputTokens: params.OutputTokens,
			}))
		}
	}

	return sb.String()
}

func toolCallIcon(tc core.ToolCall, pendingCalls []core.ToolCall, currentIdx int, parallelMode bool, spinnerView string) string {
	idx := -1
	for i, pending := range pendingCalls {
		if pending.ID == tc.ID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return "●"
	}

	// In parallel mode every in-flight call spins; sequentially, only the
	// current call does.
	if parallelMode || idx == currentIdx {
		return spinnerView
	}

	return "●"
}

// stripMarkdownHeading removes leading `#` markers from markdown headings.
func stripMarkdownHeading(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "#") {
		return line
	}
	stripped := strings.TrimLeft(trimmed, "#")
	stripped = strings.TrimPrefix(stripped, " ")
	indent := line[:len(line)-len(trimmed)]
	return indent + stripped
}

// QueuePreviewItem is the minimal data needed to render a queue item preview.
type QueuePreviewItem struct {
	Content   string
	HasImages bool
}

var (
	queueBadgeStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Accent).
			Bold(true)

	queueContentStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim)

	queueSelectedContentStyle = queueContentStyle.Foreground(kit.CurrentTheme.TextBright)

	queueSelectedBadgeStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextBright).
				Bold(true)

	queueOverflowStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Muted).
				Italic(true)
)

// RenderQueuePreview renders queued input items above the input area.
// selectedIdx is the currently selected item index (-1 = none).
func RenderQueuePreview(items []QueuePreviewItem, selectedIdx, width int) string {
	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder

	maxVisible := 5
	startIdx := 0
	if len(items) > maxVisible && selectedIdx >= maxVisible {
		startIdx = selectedIdx - maxVisible + 1
	}
	endIdx := min(startIdx+maxVisible, len(items))

	for i := startIdx; i < endIdx; i++ {
		item := items[i]
		isSelected := i == selectedIdx

		content := truncateQueueContent(item.Content, width-8)
		if item.HasImages {
			content = PendingImageStyle.Render("[Image] ") + content
		}

		if isSelected {
			bar := kit.FocusBarStyle().Render(kit.FocusBar)
			num := queueSelectedBadgeStyle.Render(fmt.Sprintf("%d.", i+1))
			preview := queueSelectedContentStyle.Render(content)
			fmt.Fprintf(&sb, " %s %s %s\n", bar, num, preview)
		} else {
			badge := queueBadgeStyle.Render(fmt.Sprintf("  %d.", i+1))
			preview := queueContentStyle.Render(content)
			fmt.Fprintf(&sb, " %s %s\n", badge, preview)
		}
	}

	if len(items) > maxVisible {
		if endIdx < len(items) {
			sb.WriteString(queueOverflowStyle.Render(fmt.Sprintf("     +%d more below", len(items)-endIdx)) + "\n")
		}
		if startIdx > 0 {
			above := queueOverflowStyle.Render(fmt.Sprintf("     +%d more above", startIdx)) + "\n"
			return above + sb.String()
		}
	}

	return sb.String()
}

// renderQueueBadge renders a compact badge for the status bar.
func renderQueueBadge(count int) string {
	if count == 0 {
		return ""
	}
	return queueBadgeStyle.Render(fmt.Sprintf(" [%d queued]", count))
}

func truncateQueueContent(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")

	if maxLen <= 0 {
		maxLen = 40
	}

	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
