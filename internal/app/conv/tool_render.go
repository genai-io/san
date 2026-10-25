package conv

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

var (
	headerStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(kit.CurrentTheme.Border).
			Padding(0, 1)

	truncatedStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Error)
)

// RenderToolResultInline renders a tool result inline (without leading newline).
func RenderToolResultInline(data ToolResultData, mdRenderer *MDRenderer) string {
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}
	data.Content = withoutRecall(data.Content)
	// File-change failures retain their requested-change preview, and Bash
	// failures retain their line-count summary. Every other nested failure can
	// use the shared layout without repeating the call's tool name.
	if data.Nested && data.IsError {
		switch toolName {
		case tool.ToolBash, tool.ToolPowerShell, tool.ToolEdit, tool.ToolWrite, tool.ToolAgent:
		default:
			return renderNestedFailure(data)
		}
	}

	switch toolName {
	case tool.ToolBash, tool.ToolPowerShell:
		if data.Nested {
			return renderBashToolResultInline(data)
		}
		return renderGenericToolResultInline(data)
	case tool.ToolSkill:
		return renderSkillResultInline(data)
	case tool.ToolAgent:
		return renderTaskResultInline(data, mdRenderer)
	case tool.ToolEdit, tool.ToolWrite:
		if data.Nested {
			return renderNestedFileChangeResultInline(data)
		}
		return renderFileChangeResultInline(data)
	case tool.ToolRead:
		if data.Nested {
			return renderNestedReadResultInline(data)
		}
		return renderGenericToolResultInline(data)
	case tool.ToolAskUserQuestion:
		return renderAskUserResultInline(data)
	}
	if data.Nested {
		return renderNestedGenericToolResultInline(data)
	}
	return renderGenericToolResultInline(data)
}

const (
	// Nested markers share a display column across command, body, and trailer rows.
	nestedBodyPrefix    = "  ┊ "
	nestedTrailerPrefix = "  └ "
)

func renderNestedReadResultInline(data ToolResultData) string {
	if data.IsError {
		return renderNestedFailure(data)
	}

	content := strings.TrimSuffix(data.Content, "\n")
	var sb strings.Builder
	if data.Expanded {
		sb.WriteString(renderNestedToolBody(content, data.Width))
	}
	sb.WriteString(renderNestedToolTrailer(formatReadResultSummary(content), toolResultStyle))
	return sb.String()
}

// renderNestedToolBody keeps visible content under the same connector that
// ends at the adjacent terminal summary. Empty content adds no decorative row.
func renderNestedToolBody(content string, width int) string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return ""
	}

	var sb strings.Builder
	for line := range strings.SplitSeq(content, "\n") {
		line = visibleLine(line)
		if strings.TrimSpace(line) == "" {
			sb.WriteString(strings.Repeat(" ", lipgloss.Width(nestedBodyPrefix)) + "\n")
			continue
		}
		sb.WriteString(renderNestedToolBodyLine(line, width))
	}
	return sb.String()
}

// visibleLine drops the progress a tool drew in place and then wrote over —
// git's "Rebasing (1/3)\r…". Left in, the carriage return lands inside the
// gutter and the terminal restarts the row at column 0, connector and all. A
// trailing one is CRLF, not an overwrite.
func visibleLine(line string) string {
	line = strings.TrimSuffix(line, "\r")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		return line[i+1:]
	}
	return line
}

func renderNestedToolBodyLine(line string, width int) string {
	return renderGutteredLine(nestedBodyPrefix, nestedBodyPrefix, toolResultStyle, visibleLine(line), width)
}

// renderGutteredLine soft-wraps one row to the terminal width so every
// wrapped row keeps its gutter (wrapPrefix). Left to the terminal, a wrapped
// row restarts at column 0 and the connector breaks. Tabs expand to lipgloss's
// tab width up front so the wrap budget matches what Render will draw.
func renderGutteredLine(prefix, wrapPrefix string, style lipgloss.Style, line string, width int) string {
	if width <= 0 {
		width = 80
	}
	line = strings.ReplaceAll(line, "\t", "    ")
	wrapWidth := max(1, width-lipgloss.Width(prefix))

	var sb strings.Builder
	for segment := range strings.SplitSeq(xansi.Hardwrap(line, wrapWidth, true), "\n") {
		sb.WriteString(style.Render(prefix+segment) + "\n")
		prefix = wrapPrefix
	}
	return sb.String()
}

// renderBashRow keeps the guide quieter than its content and aligned with "$".
func renderBashRow(marker, content string, style lipgloss.Style, width int) string {
	return strings.Repeat(" ", kit.CommandMarkerColumn(width)) +
		lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDisabled).Render(marker) + " " + style.Render(content) + "\n"
}

func bashOutputRoom(width int) int {
	if width <= 0 {
		width = 80
	}
	return max(1, width-1-kit.CommandMarkerColumn(width)-2)
}

func bashOutputLineCount(output string) int {
	if output == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(output, "\n"), "\n") + 1
}

// renderBashOutput wraps full output without discarding blank lines. A settled
// result closes on its last physical row; a running body keeps the guide open.
func renderBashOutput(content string, width int, style lipgloss.Style, closed bool) string {
	if content == "" {
		return ""
	}
	rows := strings.Split(xansi.Hardwrap(strings.ReplaceAll(visibleBashOutput(content), "\t", "    "), bashOutputRoom(width), true), "\n")
	var sb strings.Builder
	for i, row := range rows {
		marker := "│"
		if closed && i == len(rows)-1 {
			marker = "└"
		}
		sb.WriteString(renderBashRow(marker, row, style, width))
	}
	return sb.String()
}

func visibleBashOutput(output string) string {
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	for i, line := range lines {
		lines[i] = visibleLine(line)
	}
	return strings.Join(lines, "\n")
}

// Counts refer only to whole omitted output rows. A byte-capped partial line
// still needs an omission marker, but must not invent an omitted-line count.
func renderBashOmission(output, preview string, width int, details toolresult.BashDetails) string {
	shown := bashOutputLineCount(preview)
	if bashOutputLineCount(output) <= shown && details.LineCount <= shown && !details.TailTruncated {
		return ""
	}
	label := "…"
	if omitted := details.LineCount - shown; omitted > 0 {
		counted := fmt.Sprintf("… (%d)", omitted)
		if lipgloss.Width(counted) <= bashOutputRoom(width) {
			label = counted
		}
	}
	return renderBashRow("│", label, lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDisabled), width)
}

func renderNestedToolTrailer(summary string, style lipgloss.Style) string {
	return style.Render(nestedTrailerPrefix+summary) + "\n"
}

func renderNestedFailure(data ToolResultData) string {
	content := strings.TrimPrefix(data.Content, "Error: ")
	if !data.Expanded {
		content = compactFailureReason(data.Content, data.Width)
	}
	return renderNestedToolBody(content, data.Width) + renderNestedToolTrailer("failed", errorStyle)
}

func compactFailureReason(content string, width int) string {
	if _, reason, ok := splitBashFailureContent(content); ok {
		content = reason
	}
	line, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	if width <= 0 {
		width = 80
	}
	return kit.TruncateText(visibleLine(line), max(1, width-8))
}

func renderNestedGenericToolResultInline(data ToolResultData) string {
	if data.IsError {
		return renderNestedFailure(data)
	}

	content := strings.TrimSuffix(data.Content, "\n")
	var sb strings.Builder
	if data.Expanded {
		sb.WriteString(renderNestedToolBody(content, data.Width))
	}
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}
	sb.WriteString(renderNestedToolTrailer(formatToolResultSize(toolName, content), toolResultStyle))
	return sb.String()
}

func formatReadResultSummary(content string) string {
	count := 0
	for line := range strings.SplitSeq(content, "\n") {
		prefix, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(prefix)); err == nil {
			count++
		}
	}
	if count > 0 {
		return formatLineCountValue(count)
	}
	if strings.HasPrefix(content, "file exists but is empty:") {
		return "empty file"
	}
	if strings.HasPrefix(content, "no lines at offset ") {
		return "no lines"
	}
	if strings.HasPrefix(content, "Binary file detected:") {
		return "binary file"
	}
	if strings.HasPrefix(content, "image file:") {
		return "image file"
	}
	return formatLineCount(content)
}

func renderBashToolResultInline(data ToolResultData) string {
	content := data.Content
	details, hasDetails := data.Details.(toolresult.BashDetails)
	if details.TaskID != "" {
		body := ""
		if data.Expanded {
			body = renderBashOutput(content, data.Width, toolResultStyle, false)
		}
		return body + renderBashOutput("started in background · "+details.TaskID, data.Width, toolResultStyle, true)
	}

	summary := ""
	if data.IsError {
		content, summary = formatBashFailure(content, data.Details)
	}
	tail := content
	if hasDetails && details.Tail != "" {
		tail = details.Tail
	}
	if data.Expanded {
		body := renderBashOutput(content, data.Width, toolResultStyle, !data.IsError)
		if data.IsError {
			return body + renderBashOutput(summary, data.Width, errorStyle, true)
		}
		if body == "" {
			return renderBashOutput("no output", data.Width, toolResultStyle, true)
		}
		return body
	}
	if data.IsError {
		// Error evidence keeps its diagnostic budget and wrapping; the failure
		// footer, not the last diagnostic, closes the result.
		preview := toolresult.LastLines(tail, setting.MaxBashPreviewLines)
		return renderBashOmission(tail, preview, data.Width, details) +
			renderBashOutput(preview, data.Width, toolResultStyle, false) +
			renderBashOutput(compactFailureReason(summary, data.Width), data.Width, errorStyle, true)
	}
	if hasDetails && details.LineCount == 0 || tail == "" {
		return renderBashOutput("no output", data.Width, toolResultStyle, true)
	}
	if data.BashPreviewLines <= 0 {
		return renderBashOutput("completed", data.Width, toolResultStyle, true)
	}
	return renderOutputPreview(tail, data.BashPreviewLines, data.Width, details, true)
}

// renderOutputPreview keeps the last n actual output rows. Only a completed
// success closes its last row; live previews retain an open guide.
func renderOutputPreview(output string, n, width int, details toolresult.BashDetails, closed bool) string {
	if n <= 0 || output == "" {
		return ""
	}
	preview := toolresult.LastLines(output, n)
	var sb strings.Builder
	sb.WriteString(renderBashOmission(output, preview, width, details))
	lines := strings.Split(strings.TrimSuffix(preview, "\n"), "\n")
	for i, line := range lines {
		line = strings.ReplaceAll(xansi.Strip(visibleLine(line)), "\t", "    ")
		marker := "│"
		if closed && i == len(lines)-1 {
			marker = "└"
		}
		sb.WriteString(renderBashRow(marker, kit.TruncateText(line, bashOutputRoom(width)), toolResultStyle, width))
	}
	return sb.String()
}

func formatBashFailure(content string, details any) (string, string) {
	if bashDetails, ok := details.(toolresult.BashDetails); ok {
		content = trimBashErrorSuffix(content, bashDetails.Error)
		return content, formatBashFailureSummary(bashDetails.Error)
	}
	if output, reason, ok := splitBashFailureContent(content); ok {
		return output, formatBashFailureSummary(reason)
	}
	return content, "failed"
}

func formatBashFailureSummary(reason string) string {
	summary := "failed"
	if reason = formatBashFailureReason(reason); reason != "" {
		summary += " · " + reason
	}
	return summary
}

func splitBashFailureContent(content string) (output, reason string, ok bool) {
	if reason, ok := strings.CutPrefix(content, "Error: "); ok {
		return "", reason, true
	}
	const separator = "\nError: "
	index := strings.LastIndex(content, separator)
	if index < 0 {
		return "", "", false
	}
	return content[:index], content[index+len(separator):], true
}

func trimBashErrorSuffix(content, errorMessage string) string {
	withoutTerminator := strings.TrimSuffix(content, "\n")
	if withoutTerminator == "Error: "+errorMessage {
		return ""
	}
	if output, ok := strings.CutSuffix(withoutTerminator, "\nError: "+errorMessage); ok {
		return output
	}
	return content
}

func formatBashFailureReason(reason string) string {
	const timeoutPrefix = "command timed out after "
	if after, ok := strings.CutPrefix(reason, timeoutPrefix); ok {
		duration, _, _ := strings.Cut(after, " — ")
		return "timed out after " + duration
	}
	return reason
}

func renderNestedFileChangeResultInline(data ToolResultData) string {
	width := data.Width
	if width <= 0 {
		width = 80
	}

	var sb strings.Builder
	if data.IsError {
		sb.WriteString(renderFileChangeInputPreview(data.ToolInput, data.Content, width))
		data.Width = width
		sb.WriteString(renderNestedFailure(data))
		return sb.String()
	}

	if details, ok := data.Details.(toolresult.FileChangeDetails); ok {
		if !data.CollapseDiff {
			block, _ := renderStoredFileDiffIndented(details.UnifiedDiff, width, 0, "  ")
			sb.WriteString(block)
			if details.TruncatedDiffLines > 0 {
				sb.WriteString(truncatedStyle.Render(fmt.Sprintf("  … diff truncated (%d more lines)", details.TruncatedDiffLines)) + "\n")
			}
		}
		sb.WriteString(renderNestedToolTrailer(fileChangeSummary(details), toolResultStyle))
		return sb.String()
	}

	sb.WriteString(renderNestedToolTrailer(extractTrailingParenContent(data.Content, "completed"), toolResultStyle))
	return sb.String()
}

// renderFileChangeInputPreview shows the requested change when the edit did
// not apply. It uses tool input rather than diagnostics so the user can compare
// the intended replacement with the actual-file diagnostic below.
func renderFileChangeInputPreview(input, diagnostic string, width int) string {
	var params struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		OldText   string `json:"oldText"`
		NewText   string `json:"newText"`
		Edits     []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
			OldText   string `json:"oldText"`
			NewText   string `json:"newText"`
		} `json:"edits"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(input), &params) != nil {
		return ""
	}
	old, new := params.OldString, params.NewString
	if old == "" && new == "" {
		old, new = params.OldText, params.NewText
	}
	if old == "" && new == "" && len(params.Edits) > 0 {
		index := editIndexFromDiagnostic(diagnostic)
		if index < 0 || index >= len(params.Edits) {
			index = 0
		}
		edit := params.Edits[index]
		old, new = edit.OldString, edit.NewString
		if old == "" && new == "" {
			old, new = edit.OldText, edit.NewText
		}
	}
	if new == "" && params.Content != "" {
		new = params.Content
	}
	previewWidth := width - 2
	if previewWidth <= lipgloss.Width("+ ") {
		return ""
	}
	var sb strings.Builder
	for _, preview := range []struct{ marker, text string }{{"-", old}, {"+", new}} {
		if preview.text == "" {
			continue
		}
		for line := range strings.SplitSeq(preview.text, "\n") {
			for segment := range strings.SplitSeq(xansi.Wrap(line, previewWidth-lipgloss.Width(preview.marker)-1, " "), "\n") {
				sb.WriteString(toolResultStyle.Render("  "+preview.marker+" "+segment) + "\n")
			}
		}
	}
	return sb.String()
}

func editIndexFromDiagnostic(diagnostic string) int {
	const prefix = "edits["
	start := strings.Index(diagnostic, prefix)
	if start < 0 {
		return -1
	}
	start += len(prefix)
	end := strings.IndexByte(diagnostic[start:], ']')
	if end < 0 {
		return -1
	}
	index, err := strconv.Atoi(diagnostic[start : start+end])
	if err != nil {
		return -1
	}
	return index
}

func extractTrailingParenContent(s, fallback string) string {
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return fallback
	}

	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				candidate := s[i+1 : end]
				if strings.Contains(candidate, " lines") || strings.Contains(candidate, "replacement(s)") {
					return candidate
				}
				return fallback
			}
		}
	}
	return fallback
}

func fileChangeSummary(details toolresult.FileChangeDetails) string {
	addedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	if details.IsNewFile {
		lines := formatLineCountValue(details.AddedLines)
		if details.AddedLines > 0 {
			lines = addedStyle.Render("+" + lines)
		}
		return "new file · " + lines
	}
	var counts []string
	if details.AddedLines > 0 {
		counts = append(counts, addedStyle.Render(fmt.Sprintf("+%d", details.AddedLines)))
	}
	if details.RemovedLines > 0 {
		counts = append(counts, errorStyle.Render(fmt.Sprintf("-%d", details.RemovedLines)))
	}
	if len(counts) == 0 {
		return "no changes"
	}
	return strings.Join(counts, " ")
}

func renderFileChangeResultInline(data ToolResultData) string {
	if data.IsError {
		// Same "failed" summary + indented reason as the generic error branch;
		// only the redundant "Error: " prefix is dropped first.
		data.Content = strings.TrimPrefix(data.Content, "Error: ")
		return renderGenericToolResultInline(data)
	}
	details, ok := data.Details.(toolresult.FileChangeDetails)
	if !ok {
		return renderGenericToolResultInline(data)
	}

	summary := fileChangeSummary(details)

	var sb strings.Builder
	sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  %s → %s", toolResultIcon(false), data.ToolName, summary)) + "\n")
	if data.CollapseDiff {
		return sb.String()
	}

	width := data.Width
	if width <= 0 {
		width = 80
	}
	// The diff renders in full: scrollback freezes, so anything hidden here
	// would be lost for good. The only bound is the cap applied when the
	// diff was stored, reported below from its structured count.
	block, _ := RenderStoredFileDiff(details.UnifiedDiff, width, 0)
	sb.WriteString(block)
	if details.TruncatedDiffLines > 0 {
		sb.WriteString(truncatedStyle.Render(fmt.Sprintf("     … diff truncated (%d more lines)", details.TruncatedDiffLines)) + "\n")
	}
	return sb.String()
}

func renderGenericToolResultInline(data ToolResultData) string {
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}
	sizeInfo := formatToolResultSize(toolName, data.Content)
	if data.IsError {
		sizeInfo = "failed"
	}
	icon := toolResultIcon(data.IsError)

	summaryStyle := toolResultStyle
	if data.IsError {
		summaryStyle = errorStyle
	}

	var sb strings.Builder
	sb.WriteString(summaryStyle.Render(fmt.Sprintf("  %s  %s → %s", icon, toolName, sizeInfo)) + "\n")
	content := data.Content
	if data.IsError && !data.Expanded {
		content = compactFailureReason(content, data.Width)
	}
	if data.Expanded || data.IsError {
		for line := range strings.SplitSeq(content, "\n") {
			if data.IsError {
				line = " " + line
			}
			sb.WriteString(toolResultExpandedStyle.Render(line) + "\n")
		}
	}
	return sb.String()
}

// askQuestionTexts reads the tool call's questions without changing the tool
// result sent to the model. The transcript uses them to show answers without
// repeating each question.
func askQuestionTexts(input string) []string {
	var params struct {
		Question  string `json:"question"`
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	if json.Unmarshal([]byte(input), &params) != nil {
		return nil
	}
	if len(params.Questions) > 0 {
		questions := make([]string, 0, len(params.Questions))
		for _, q := range params.Questions {
			questions = append(questions, q.Question)
		}
		return questions
	}
	if params.Question == "" {
		params.Question = "Please choose:"
	}
	return []string{params.Question}
}

func renderAskUserCall(input, icon string, width int) string {
	questions := askQuestionTexts(input)
	if len(questions) == 0 {
		questions = []string{"Question"}
	}
	var sb strings.Builder
	for i, question := range questions {
		prefix := icon + " "
		if i > 0 {
			prefix = "  "
		}
		if len(questions) > 1 {
			question = fmt.Sprintf("Q%d: %s", i+1, question)
		}
		for line := range strings.SplitSeq(question, "\n") {
			sb.WriteString(renderGutteredLine(prefix, "  ", questionCallStyle, line, width))
			prefix = "  "
		}
	}
	return sb.String()
}

func renderAskUserResultInline(data ToolResultData) string {
	if data.IsError {
		return toolResultStyle.Render(fmt.Sprintf("  ✗  %s", data.Content)) + "\n"
	}
	if strings.Contains(data.Content, "User cancelled") {
		return toolResultStyle.Render("  ↳ Cancelled") + "\n"
	}
	if data.Content == "User did not select any option." {
		return toolResultStyle.Render("  ↳ No selection") + "\n"
	}

	questions := askQuestionTexts(data.ToolInput)
	var answers []string
	for line := range strings.SplitSeq(data.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "User responses:" {
			continue
		}
		for i, question := range questions {
			if answer, ok := strings.CutPrefix(line, question+" → "); ok {
				line = answer
				if len(questions) > 1 {
					line = fmt.Sprintf("Q%d: %s", i+1, answer)
				}
				break
			}
		}
		answers = append(answers, line)
	}

	if len(answers) == 0 {
		return toolResultStyle.Render("  ↳ Answered") + "\n"
	}

	var sb strings.Builder
	for _, answer := range answers {
		sb.WriteString(toolResultStyle.Render("  ↳ "+answer) + "\n")
	}
	return sb.String()
}

func renderSkillResultInline(data ToolResultData) string {
	icon := toolResultIcon(data.IsError)

	var sb strings.Builder
	if data.IsError {
		summary := toolResultStyle.Render(fmt.Sprintf("  %s  %s", icon, data.Content))
		sb.WriteString(summary + "\n")
		return sb.String()
	}

	skillName, scriptCount, refCount := parseSkillResultContent(data.Content)
	resources := make([]string, 0, 2)
	if scriptCount > 0 {
		if scriptCount == 1 {
			resources = append(resources, "1 script")
		} else {
			resources = append(resources, fmt.Sprintf("%d scripts", scriptCount))
		}
	}
	if refCount > 0 {
		if refCount == 1 {
			resources = append(resources, "1 ref")
		} else {
			resources = append(resources, fmt.Sprintf("%d refs", refCount))
		}
	}

	result := fmt.Sprintf("Loaded: %s", skillName)
	if len(resources) > 0 {
		result += fmt.Sprintf(" [%s]", strings.Join(resources, ", "))
	}

	summary := toolResultStyle.Render(fmt.Sprintf("  %s  %s", icon, result))
	sb.WriteString(summary + "\n")

	if data.Expanded {
		for line := range strings.SplitSeq(data.Content, "\n") {
			sb.WriteString(toolResultExpandedStyle.Render(line) + "\n")
		}
	}

	return sb.String()
}

func renderTaskResultInline(data ToolResultData, mdRenderer *MDRenderer) string {
	if details, ok := data.Details.(toolresult.AgentDetails); ok {
		return renderAgentResultDetails(data, details, mdRenderer)
	}
	// Legacy transcripts stored the display header in the model-facing text.
	var sb strings.Builder
	content := data.Content

	if data.IsError {
		if data.Nested {
			return renderNestedFailure(data)
		}
		return renderGenericToolResultInline(data)
	}

	taskID := extractField(content, "Task ID: ", "")
	isBackground := strings.Contains(content, "started in background")
	if isBackground && taskID != "" {
		return renderAgentBackgroundStart(taskID, data.Width)
	}

	toolUses := extractIntField(content, "ToolUses: ")
	tokens := extractIntField(content, "Tokens: ")
	if tokens == 0 {
		var input, output int
		if _, err := fmt.Sscanf(extractField(content, "Tokens: ", ""), "in=%d out=%d", &input, &output); err == nil {
			tokens = input + output
		}
	}
	duration := extractField(content, "Duration: ", "")
	resultModel := extractField(content, "Model: ", "\n")
	doneStats := buildDoneStats(toolUses, tokens, duration, resultModel)

	if !data.Expanded {
		body := content
		if strings.HasPrefix(body, "Agent ") || strings.HasPrefix(body, "Agent:") || strings.HasPrefix(body, "ToolUses:") {
			_, body, _ = strings.Cut(body, "\n\n")
			_, body = splitByProcessCount(body, extractIntField(content, "Process: "))
		}
		return renderCompactAgentResponse(body, data.Width)
	}

	if data.ToolInput != "" {
		w := 80
		if mdRenderer != nil {
			w = mdRenderer.width
		}
		sb.WriteString(formatAgentDefinition(parseAgentInput(data.ToolInput), w))
	}

	body := ""
	if _, rest, found := strings.Cut(content, "\n\n"); found {
		body = rest
	}
	processCount := extractIntField(content, "Process: ")
	process, response := splitByProcessCount(body, processCount)

	if process != "" {
		for line := range strings.SplitSeq(process, "\n") {
			sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  %s", line)) + "\n")
		}
	}

	if response != "" {
		sb.WriteString(agentLabelStyle.Render("  ⎿  Response:") + "\n")
		rendered := response
		if mdRenderer != nil {
			if md, err := mdRenderer.agentBodyRenderer().Render(response); err == nil {
				rendered = strings.TrimSpace(md)
			}
		}
		for line := range strings.SplitSeq(rendered, "\n") {
			sb.WriteString(toolResultExpandedStyle.Render(agentContentIndent+line) + "\n")
		}
	}

	resultLine := "  ⎿  Done"
	if doneStats != "" {
		resultLine += " (" + doneStats + ")"
	}
	sb.WriteString(toolResultStyle.Render(resultLine) + "\n")
	return sb.String()
}

func renderAgentResultDetails(data ToolResultData, details toolresult.AgentDetails, mdRenderer *MDRenderer) string {
	if details.TaskID != "" {
		return renderAgentBackgroundStart(details.TaskID, data.Width)
	}
	if !data.Expanded {
		if details.Stopped || data.IsError {
			status, style := "Failed", errorStyle
			if details.Stopped {
				status, style = "Stopped", toolResultStyle
			}
			reason := details.Error
			if reason == "" {
				reason = data.Content
			}
			if reason != "" && !(details.Stopped && kit.IsRoutineStopReason(reason)) {
				status += " · " + reason
			}
			return style.Render("  "+compactFailureReason(status, data.Width)) + "\n"
		}
		return renderCompactAgentResponse(data.Content, data.Width)
	}
	status, style := "Finished", toolResultStyle
	if data.IsError || details.Stopped {
		status, style = "Failed", errorStyle
		if details.Stopped {
			status, style = "Stopped", toolResultStyle
		}
		if details.Error != "" && !(details.Stopped && kit.IsRoutineStopReason(details.Error)) {
			status += " · " + compactFailureReason(details.Error, data.Width)
		}
	}
	if stats := buildDoneStats(details.ToolUses, details.InputTokens+details.OutputTokens, toolresult.FormatDuration(details.Duration), details.Model); stats != "" {
		status += " (" + stats + ")"
	}
	var sb strings.Builder
	width := data.Width
	if width <= 0 {
		width = 80
	}
	sb.WriteString(formatAgentDefinition(parseAgentInput(data.ToolInput), width))
	activity := details.Activity
	const maxActivityLines = 30
	if len(activity) > maxActivityLines {
		sb.WriteString(renderNestedToolBody(fmt.Sprintf("(%d earlier tool calls omitted)", len(activity)-maxActivityLines), data.Width))
		activity = activity[len(activity)-maxActivityLines:]
	}
	sb.WriteString(renderNestedToolBody(strings.Join(activity, "\n"), data.Width))
	response := data.Content
	if mdRenderer != nil {
		if md, err := mdRenderer.agentBodyRenderer().Render(response); err == nil {
			response = strings.TrimSpace(md)
		}
	}
	sb.WriteString(renderNestedToolBody(response, data.Width))
	if data.Nested {
		sb.WriteString(style.Render(nestedTrailerPrefix + status))
	} else {
		sb.WriteString(style.Render(fmt.Sprintf("  %s  Agent → %s", toolResultIcon(data.IsError && !details.Stopped), status)))
	}
	sb.WriteString("\n")
	return sb.String()
}

func renderAgentBackgroundStart(taskID string, width int) string {
	return renderGutteredLine(nestedTrailerPrefix, "    ", toolResultStyle, "Started in background · "+taskID, width)
}

func renderAgentLocalBody(body string, width int) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	var sb strings.Builder
	for _, line := range strings.Split(xansi.Hardwrap(body, max(1, width-2), true), "\n") {
		sb.WriteString(toolResultStyle.Render("  "+line) + "\n")
	}
	return sb.String()
}

// renderCompactAgentResponse uses only worker prose, never a synthesized summary.
func renderCompactAgentResponse(response string, width int) string {
	response = strings.TrimSpace(response)
	// Older details do not retain inspection pointers. Only recognize the paired
	// generated suffix when the transcript filename corroborates the agent ID.
	parts := strings.Split(response, "\n\n")
	if len(parts) >= 3 {
		id, hasID := strings.CutPrefix(parts[len(parts)-2], "AgentID: ")
		path, hasPath := strings.CutPrefix(parts[len(parts)-1], "Transcript: ")
		if hasID && hasPath && id != "" && !strings.ContainsAny(id, " /\\\n\r") && !strings.ContainsAny(path, "\n\r") && strings.HasSuffix(path, "/"+id+".jsonl") {
			response = strings.TrimSpace(strings.Join(parts[:len(parts)-2], "\n\n"))
		}
	}
	if response == "" {
		response = "No output"
	}
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(xansi.Wrap(response, max(1, width-2), " "), "\n")
	omitted := len(lines) > 8
	if omitted {
		lines = lines[:8]
	}
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(toolCallStyle.Render("  "+line) + "\n")
	}
	if omitted {
		sb.WriteString(toolResultStyle.Render("  … more output omitted") + "\n")
	}
	return sb.String()
}

func splitByProcessCount(body string, processCount int) (process, response string) {
	if body == "" {
		return "", ""
	}
	if processCount <= 0 {
		return "", strings.TrimSpace(body)
	}

	lines := strings.SplitN(body, "\n", processCount+1)
	if len(lines) <= processCount {
		return strings.TrimSpace(strings.Join(lines, "\n")), ""
	}
	processLines := lines[:processCount]
	rest := lines[processCount]
	return strings.TrimSpace(strings.Join(processLines, "\n")), strings.TrimSpace(rest)
}

func formatAgentDefinition(agent agentInput, width int) string {
	if !agent.Valid {
		return ""
	}

	var sb strings.Builder
	meta := make([]string, 0, 2)
	if agent.Mode != "" {
		meta = append(meta, fmt.Sprintf("mode=%s", agent.Mode))
	}
	if agent.Background {
		meta = append(meta, "background")
	}
	if len(meta) > 0 {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  [%s]", strings.Join(meta, ", "))) + "\n")
	}

	if agent.Prompt != "" {
		sb.WriteString(agentLabelStyle.Render("  ⎿  Prompt:") + "\n")
		wrapWidth := width - lipgloss.Width(agentContentIndent)
		for line := range strings.SplitSeq(agent.Prompt, "\n") {
			for _, wrapped := range wrapLine(line, wrapWidth) {
				sb.WriteString(toolResultExpandedStyle.Render(agentContentIndent+wrapped) + "\n")
			}
		}
	}

	return sb.String()
}

func wrapLine(line string, width int) []string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return []string{line}
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	curWidth := lipgloss.Width(current)
	for _, w := range words[1:] {
		ww := lipgloss.Width(w)
		if curWidth+1+ww > width {
			lines = append(lines, current)
			current = w
			curWidth = ww
		} else {
			current += " " + w
			curWidth += 1 + ww
		}
	}
	return append(lines, current)
}

func buildDoneStats(toolUses, tokens int, duration, model string) string {
	stats := make([]string, 0, 4)
	if toolUses == 1 {
		stats = append(stats, "1 tool use")
	} else if toolUses > 1 {
		stats = append(stats, fmt.Sprintf("%d tool uses", toolUses))
	}
	if tokens > 0 {
		stats = append(stats, kit.FormatTokenCount(tokens)+" tokens")
	}
	if duration != "" {
		stats = append(stats, duration)
	}
	if model != "" {
		stats = append(stats, model)
	}
	return strings.Join(stats, " · ")
}

func parseSkillResultContent(content string) (skillName string, scriptCount, refCount int) {
	skillName = "skill"
	if idx := strings.Index(content, `<skill-invocation name="`); idx != -1 {
		start := idx + len(`<skill-invocation name="`)
		if end := strings.Index(content[start:], `"`); end != -1 {
			skillName = content[start : start+end]
		}
	}

	if idx := strings.Index(content, "Available scripts"); idx != -1 {
		section := content[idx:]
		lines := strings.Split(section, "\n")
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if strings.HasPrefix(line, "  - ") {
				scriptCount++
			} else if line == "" || !strings.HasPrefix(line, " ") {
				break
			}
		}
	}

	if idx := strings.Index(content, "Reference files"); idx != -1 {
		section := content[idx:]
		lines := strings.Split(section, "\n")
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if strings.HasPrefix(line, "  - ") {
				refCount++
			} else if line == "" || !strings.HasPrefix(line, " ") {
				break
			}
		}
	}

	return skillName, scriptCount, refCount
}

func extractField(content, prefix, defaultVal string) string {
	idx := strings.Index(content, prefix)
	if idx == -1 {
		return defaultVal
	}
	start := idx + len(prefix)
	end := strings.Index(content[start:], "\n")
	if end == -1 {
		return content[start:]
	}
	return content[start : start+end]
}

func extractIntField(content, prefix string) int {
	val := extractField(content, prefix, "")
	if val == "" {
		return 0
	}
	end := 0
	for end < len(val) && val[end] >= '0' && val[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(val[:end])
	return n
}

func formatAgentLabel(agent agentInput) string {
	if !agent.Valid {
		return "Agent"
	}

	desc := ""
	if agent.Description != "" {
		desc = conciseAgentDescription(agent.Description)
	} else if agent.Prompt != "" {
		desc = conciseAgentDescription(agent.Prompt)
	}

	name := "Agent"
	if agent.Name != "" && agent.Name != "Agent" {
		name += " · " + agent.Name
	}
	if desc != "" {
		return name + " · " + desc
	}
	return name
}

// sendMessageLabel names a SendMessage call by its recipient and opening words.
func sendMessageParts(input string) (to, subject string) {
	var p struct{ To, Subject string }
	_ = json.Unmarshal([]byte(input), &p)
	return strings.TrimPrefix(p.To, "@"), p.Subject
}

// memberPalette tells group members apart: "To @api" and "From @api" share
// @api's colour, so one exchange reads as a thread.
var memberPalette = []kit.AdaptiveColor{
	{Dark: "#93C5FD", Light: "#1D4ED8"}, // blue
	{Dark: "#F0ABFC", Light: "#A21CAF"}, // magenta
	{Dark: "#5EEAD4", Light: "#0F766E"}, // teal
	{Dark: "#FCD34D", Light: "#B45309"}, // amber
	{Dark: "#C4B5FD", Light: "#6D28D9"}, // violet
	{Dark: "#FDA4AF", Light: "#BE123C"}, // rose
}

// memberColor is the slot the session gave name; a name it has not placed
// yet (a resume before the roster is read) falls back to a hash.
func memberColor(name string, colors map[string]int) kit.AdaptiveColor {
	if i, ok := colors[name]; ok {
		return memberPalette[i%len(memberPalette)]
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return memberPalette[h.Sum32()%uint32(len(memberPalette))]
}

// renderMemberLine draws one message to or from a group member as a single
// line: the icon and @name in the member's colour, then the body cut to the
// width, keeping a trailing " · note" whole.
func renderMemberLine(icon, dir, name, body string, width int, colors map[string]int) string {
	color := lipgloss.NewStyle().Foreground(memberColor(name, colors))
	note := ""
	if i := strings.LastIndex(body, " · "); i >= 0 {
		body, note = body[:i], toolResultStyle.Render(body[i:])
	}
	head := color.Render(icon+" ") + toolCallStyle.Render(dir+" ") + color.Bold(true).Render("@"+name)
	if body == "" { // no subject: who it is to or from says it all
		return head + note
	}
	head += toolCallStyle.Render(":")
	room := width - lipgloss.Width(head) - lipgloss.Width(note) - 2
	if room < 8 {
		return head + note
	}
	return head + toolCallStyle.Render(" "+kit.TruncateText(body, room)) + note
}

// groupCallArgs names a Group call by what it does, e.g. "join shop as web".
func groupCallArgs(input string) string {
	var p struct {
		Action, Group, As string
		Hold              bool
	}
	_ = json.Unmarshal([]byte(input), &p)
	parts := []string{p.Action}
	switch p.Action {
	case "join":
		parts = append(parts, p.Group)
		if p.As != "" {
			parts = append(parts, "as", p.As)
		}
		if p.Hold {
			parts = append(parts, "holding")
		}
	case "hold":
		if p.Hold {
			parts = append(parts, "on")
		} else {
			parts = append(parts, "off")
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func conciseAgentDescription(desc string) string {
	return strings.Join(strings.Fields(desc), " ")
}

func displayAgentName(agentName, mode string) string {
	if strings.TrimSpace(agentName) == "" {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "explore":
			return "Explorer"
		case "edit":
			return "Editor"
		}
		return "Agent"
	}
	return shortAgentName(agentName)
}

type agentInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Mode        string `json:"mode"`
	Background  bool   `json:"run_in_background"`
	Valid       bool   `json:"-"`
}

func parseAgentInput(input string) agentInput {
	var agent agentInput
	if err := json.Unmarshal([]byte(input), &agent); err != nil {
		return agentInput{}
	}
	agent.Valid = true
	if agent.Name == "" {
		agent.Name = displayAgentName("", agent.Mode)
	}
	return agent
}

func shortAgentName(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	kept := make([]string, 0, 2)
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" || word == "current" || word == "change" || word == "changes" {
			continue
		}
		kept = append(kept, word)
		if len(kept) == 2 {
			break
		}
	}
	if len(kept) == 0 {
		return "Agent"
	}
	for i, word := range kept {
		kept[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(kept, " ")
}

// taskGetIsList reports whether a TaskGet call is a list-all invocation (no
// taskId) rather than a single-task lookup.
func taskGetIsList(input string) bool {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return false
	}
	id, _ := params["taskId"].(string)
	return id == ""
}

func extractTaskGetDisplay(input string, ownerMap map[string]string) string {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}
	id, _ := params["taskId"].(string)
	if owner, ok := ownerMap[id]; ok && owner != "" {
		return owner
	}
	return id
}

func extractToolArgs(input string) string {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}

	if fp, ok := params["file_path"].(string); ok {
		return fp
	}
	if c, ok := params["command"].(string); ok {
		return c
	}
	if p, ok := params["pattern"].(string); ok {
		return p
	}
	if p, ok := params["path"].(string); ok {
		return p
	}
	if u, ok := params["url"].(string); ok {
		return u
	}
	if s, ok := params["skill"].(string); ok {
		return s
	}
	if qs, ok := params["questions"].([]any); ok {
		count := len(qs)
		if count == 1 {
			return "1 question"
		}
		return fmt.Sprintf("%d questions", count)
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := params[k].(string); ok {
			return s
		}
	}
	return ""
}

func formatToolResultSize(toolName, content string) string {
	switch toolName {
	case "WebFetch":
		return toolresult.FormatSize(int64(len(content)))
	case "Write", "Edit":
		return extractParenContent(content, "completed")
	case tool.ToolSendMessage, tool.ToolGroup:
		// Their first clause is the outcome, e.g. "Sent · reading now".
		first, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
		first, _, _ = strings.Cut(first, "; ")
		return strings.TrimSuffix(first, ".")
	default:
		return formatLineCount(content)
	}
}

func extractParenContent(s, fallback string) string {
	start := strings.IndexByte(s, '(')
	if start == -1 {
		return fallback
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[start+1 : i]
			}
		}
	}
	return fallback
}

func formatLineCount(content string) string {
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return "no output"
	}
	return formatLineCountValue(strings.Count(trimmed, "\n") + 1)
}

func formatLineCountValue(lineCount int) string {
	if lineCount == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", lineCount)
}

// renderBashDescription dims the description in parentheses after the Bash
// label, shortening the text — never the closing paren — to fit width.
func renderBashDescription(description string, width int) string {
	inner := width - lipgloss.Width(" ()")
	if description == "" || inner < 8 { // too narrow for words: "()" or "(...)"
		return ""
	}
	return toolResultStyle.Render(" (" + xansi.Truncate(description, inner, "...") + ")")
}

func renderToolLine(label string, width int) string {
	return renderToolLineWithIcon(label, width, "●")
}

func renderToolLineWithIcon(label string, width int, iconText string) string {
	icon := toolCallStyle.Width(2).Render(iconText)
	return lipgloss.JoinHorizontal(lipgloss.Top, icon, toolCallStyle.Render(truncateToolLabel(label, width)))
}

// renderShellToolCall renders a shell tool call without ever truncating its
// command. A single-line command stays in Bash(command) form only when that
// complete label fits; its optional description may be shortened to keep the
// row within the terminal. Commands that contain a newline or do not fit become
// a full command block below the Bash header and soft-wrap without ellipses.
func renderShellToolCall(toolName, input string, width int, icon, detail string) string {
	command, description := extractBashCommand(input)
	description = strings.Join(strings.Fields(description), " ")
	labelWidth := max(1, bashPreviewLabelWidth(width)-lipgloss.Width(detail))

	if strings.TrimSpace(command) != "" && !strings.Contains(command, "\n") {
		commandLabel := fmt.Sprintf("%s(%s)", toolName, command)
		if lipgloss.Width(commandLabel) <= labelWidth {
			label := toolCallStyle.Render(commandLabel)
			label += renderBashDescription(description, labelWidth-lipgloss.Width(commandLabel))
			iconCell := toolCallStyle.Width(2).Render(icon)
			return lipgloss.JoinHorizontal(lipgloss.Top, iconCell, label) + detail + "\n"
		}
	}
	if strings.TrimSpace(command) == "" {
		command = "(no command)"
	}

	var sb strings.Builder

	// Header line: ● Bash (description) · running detail. The command is
	// rendered separately below without abbreviation.
	displayName := xansi.Truncate(toolName, labelWidth, "…")
	header := toolCallStyle.Render(displayName) + renderBashDescription(description, labelWidth-lipgloss.Width(displayName))
	iconCell := toolCallStyle.Width(2).Render(icon)
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, iconCell, header) + detail + "\n")

	sb.WriteString(kit.RenderCommandBlock(command, width))
	return sb.String()
}

// extractBashCommand pulls the command and optional description out of a Bash
// tool call's raw JSON input.
func extractBashCommand(input string) (command, description string) {
	var params struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", ""
	}
	return params.Command, params.Description
}

func renderAgentToolLine(label string, width int, iconText string, color string) string {
	style := agentStyle(color)
	icon := style.Width(2).Render(iconText)
	if width > 0 {
		label = xansi.Hardwrap(label, max(1, width-2), true)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, icon, style.Render(label))
}

func agentStyle(color string) lipgloss.Style {
	return toolCallStyle.Foreground(agentColor(color))
}

func agentColor(color string) kit.AdaptiveColor {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case "blue":
		return kit.CurrentTheme.Primary
	case "yellow":
		return kit.CurrentTheme.Warning
	case "gray", "grey":
		return kit.CurrentTheme.Muted
	case "accent":
		return kit.CurrentTheme.Accent
	case "ai":
		return kit.CurrentTheme.AI
	case "green", "":
		return kit.CurrentTheme.Success
	default:
		if strings.HasPrefix(color, "#") {
			return kit.AdaptiveColor{Dark: color, Light: color}
		}
		return kit.CurrentTheme.Success
	}
}

// configuredAgentColor returns the color set for this agent's name in its
// configuration (a name like "blue" or a "#rrggbb" hex), or "" when none is
// set. It is later resolved to a theme color by agentColor.
func configuredAgentColor(agent agentInput, colors map[string]string) string {
	if len(colors) == 0 {
		return ""
	}
	return colors[strings.ToLower(agent.Name)]
}

func truncateToolLabel(label string, width int) string {
	return kit.TruncateText(label, maxToolLabelWidth(width))
}

func bashPreviewLabelWidth(width int) int {
	if width <= 0 {
		return 80
	}
	return max(1, width-lipgloss.Width("● "))
}

func maxToolLabelWidth(width int) int {
	if width <= 0 {
		return 80
	}
	maxWidth := min(width, max(width*80/100, 50))
	return max(1, maxWidth-lipgloss.Width("● "))
}
